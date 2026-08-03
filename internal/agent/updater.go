package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dockpulse/dockpulse/internal/domain"
)

type Plan struct {
	Target   string   `json:"target"`
	Scope    string   `json:"scope"`
	Steps    []string `json:"steps"`
	Warnings []string `json:"warnings,omitempty"`
}
type Updater struct {
	Docker              Runner
	AllowedComposeRoots []string
	SelfUpdate          SelfUpdateConfig
}

type SelfUpdateConfig struct {
	Enabled       bool
	Directory     string
	Project       string
	Service       string
	TargetVersion string
}

func (u Updater) DryRun(ctx context.Context, req domain.ActionRequest) (Plan, error) {
	in, err := u.inspect(ctx, req.ContainerID)
	if err != nil {
		return Plan{}, err
	}
	if err = validateManaged(in); err != nil {
		return Plan{}, err
	}
	if in.Config.Labels["com.docker.compose.project"] != "" {
		return u.composePlan(ctx, in, req.Scope)
	}
	return u.runPlan(in)
}

func (u Updater) Execute(ctx context.Context, req domain.ActionRequest, logf func(string, string)) error {
	if !req.Confirm {
		return fmt.Errorf("explicit confirmation required")
	}
	in, err := u.inspect(ctx, req.ContainerID)
	if err != nil {
		return err
	}
	if err = validateManaged(in); err != nil {
		return err
	}
	if in.Config.Labels["com.docker.compose.project"] != "" {
		plan, err := u.composePlan(ctx, in, req.Scope)
		if err != nil {
			return err
		}
		for _, step := range plan.Steps {
			logf("info", step)
		}
		return u.executeCompose(ctx, in, req.Scope)
	}
	plan, err := u.runPlan(in)
	if err != nil {
		return err
	}
	for _, step := range plan.Steps {
		logf("info", step)
	}
	return u.executeRun(ctx, in, logf)
}

func (u Updater) SelfUpdatePlan(targetVersion string) (Plan, error) {
	cfg := u.SelfUpdate
	if !cfg.Enabled {
		return Plan{}, fmt.Errorf("self-update is disabled; set DOCKPULSE_SELF_UPDATE_ENABLED=true on this agent")
	}
	if cfg.Directory == "" || cfg.Project == "" || cfg.Service == "" {
		return Plan{}, fmt.Errorf("self-update requires DOCKPULSE_SELF_UPDATE_DIR, DOCKPULSE_SELF_UPDATE_PROJECT and DOCKPULSE_SELF_UPDATE_SERVICE")
	}
	if targetVersion == "" {
		targetVersion = cfg.TargetVersion
	}
	if targetVersion == "" {
		targetVersion = "latest"
	}
	return Plan{Target: cfg.Project + "/" + cfg.Service, Scope: "self-update", Steps: []string{"Set DOCKPULSE_VERSION=" + targetVersion + " for this Compose run", "Pull DockPulse service image: docker compose pull " + cfg.Service, "Recreate service from " + cfg.Directory + ": docker compose up -d " + cfg.Service, "The agent may briefly disconnect while the service is replaced"}, Warnings: []string{"self-update is opt-in and depends on the host Compose file using DOCKPULSE_VERSION in the image tag"}}, nil
}

func (u Updater) ExecuteSelfUpdate(ctx context.Context, targetVersion string, logf func(string, string)) error {
	plan, err := u.SelfUpdatePlan(targetVersion)
	if err != nil {
		return err
	}
	if targetVersion == "" {
		targetVersion = u.SelfUpdate.TargetVersion
	}
	if targetVersion == "" {
		targetVersion = "latest"
	}
	for _, step := range plan.Steps {
		logf("info", step)
	}
	cfg := u.SelfUpdate
	base := []string{"compose", "--project-directory", cfg.Directory, "-p", cfg.Project}
	env := []string{"DOCKPULSE_VERSION=" + targetVersion, "DOCKPULSE_UPDATE_VERSION=" + targetVersion}
	if _, err = u.runDocker(ctx, env, append(append([]string{}, base...), "pull", cfg.Service)...); err != nil {
		return err
	}
	_, err = u.runDocker(ctx, env, append(append([]string{}, base...), "up", "-d", cfg.Service)...)
	return err
}

func (u Updater) runDocker(ctx context.Context, env []string, args ...string) ([]byte, error) {
	if r, ok := u.Docker.(EnvRunner); ok {
		return r.RunWithEnv(ctx, env, args...)
	}
	return u.Docker.Run(ctx, args...)
}

func (u Updater) inspect(ctx context.Context, id string) (inspectContainer, error) {
	raw, err := u.Docker.Run(ctx, "inspect", id)
	if err != nil {
		return inspectContainer{}, err
	}
	var v []inspectContainer
	if err = json.Unmarshal(raw, &v); err != nil || len(v) != 1 {
		return inspectContainer{}, fmt.Errorf("invalid docker inspect response")
	}
	return v[0], nil
}

func validateManaged(in inspectContainer) error {
	labels := in.Config.Labels
	if labels["io.dockpulse.manage"] == "false" {
		return fmt.Errorf("management disabled by label")
	}
	sensitive := isSensitive(in.Name, in.Config.Image, labels)
	if sensitive && labels["io.dockpulse.allow-sensitive"] != "true" {
		return fmt.Errorf("sensitive workload requires io.dockpulse.allow-sensitive=true")
	}
	if labels["com.docker.compose.project"] == "" && labels["io.dockpulse.manage"] != "true" {
		return fmt.Errorf("docker-run container requires io.dockpulse.manage=true")
	}
	return nil
}

func (u Updater) composePlan(ctx context.Context, in inspectContainer, scope string) (Plan, error) {
	l := in.Config.Labels
	project, service, dir := l["com.docker.compose.project"], l["com.docker.compose.service"], l["com.docker.compose.project.working_dir"]
	if project == "" || service == "" || dir == "" {
		return Plan{}, fmt.Errorf("incomplete Compose metadata")
	}
	if err := u.validateDir(dir); err != nil {
		return Plan{}, err
	}
	if scope == "" {
		scope = "service"
	}
	if scope != "service" && scope != "stack" {
		return Plan{}, fmt.Errorf("Compose scope must be service or stack")
	}
	services := []string{service}
	if scope == "stack" {
		base := []string{"compose", "--project-directory", dir, "-p", project, "config", "--services"}
		raw, err := u.Docker.Run(ctx, base...)
		if err != nil {
			return Plan{}, fmt.Errorf("enumerate Compose services: %w", err)
		}
		services = strings.Fields(string(raw))
		if len(services) == 0 {
			return Plan{}, fmt.Errorf("Compose project contains no services")
		}
	}
	target := project
	if scope == "service" {
		target += "/" + service
	}
	suffix := ""
	if scope == "service" {
		suffix = " " + service
	}
	return Plan{Target: target, Scope: scope, Steps: []string{"Affected services: " + strings.Join(services, ", "), "Pull immutable manifest(s): docker compose pull" + suffix, "Reconcile project from " + dir + ": docker compose up -d" + suffix, "Inspect resulting container image digest and health"}}, nil
}
func (u Updater) validateDir(dir string) error {
	return validateComposeDir(dir, u.AllowedComposeRoots)
}
func (u Updater) executeCompose(ctx context.Context, in inspectContainer, scope string) error {
	l := in.Config.Labels
	dir, project, service := l["com.docker.compose.project.working_dir"], l["com.docker.compose.project"], l["com.docker.compose.service"]
	base := []string{"compose", "--project-directory", dir, "-p", project}
	pull := append(append([]string{}, base...), "pull")
	up := append(append([]string{}, base...), "up", "-d")
	if scope != "stack" {
		pull = append(pull, service)
		up = append(up, service)
	}
	if _, err := u.Docker.Run(ctx, pull...); err != nil {
		return err
	}
	_, err := u.Docker.Run(ctx, up...)
	return err
}

func (u Updater) runPlan(in inspectContainer) (Plan, error) {
	args, warnings, err := createArgs(in, strings.TrimPrefix(in.Name, "/"))
	if err != nil {
		return Plan{}, err
	}
	safe := redactArgs(args)
	return Plan{Target: strings.TrimPrefix(in.Name, "/"), Scope: "container", Steps: []string{"Pull image " + in.Config.Image, "Stop and rename the current container as a rollback candidate", "Create replacement: docker " + strings.Join(safe, " "), "Start replacement and verify Docker reports it running", "Remove rollback candidate only after successful start"}, Warnings: warnings}, nil
}

func createArgs(in inspectContainer, name string) ([]string, []string, error) {
	if in.HostConfig.AutoRemove {
		return nil, nil, fmt.Errorf("containers created with --rm cannot be updated safely because stopping deletes the rollback candidate")
	}
	if in.HostConfig.PublishAllPorts {
		return nil, nil, fmt.Errorf("containers using publish-all cannot preserve dynamically assigned host ports safely")
	}
	if len(in.HostConfig.Links) > 0 {
		return nil, nil, fmt.Errorf("containers using legacy links are not safely reconstructable")
	}
	a := []string{"create", "--name", name}
	var warnings []string
	for _, e := range in.Config.Env {
		a = append(a, "--env", e)
	}
	keys := make([]string, 0, len(in.Config.Labels))
	for k := range in.Config.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a = append(a, "--label", k+"="+in.Config.Labels[k])
	}
	for _, b := range in.HostConfig.Binds {
		a = append(a, "--volume", b)
	}
	for _, m := range in.Mounts {
		if m.Type == "tmpfs" {
			continue
		}
		if m.Type != "bind" && m.Type != "volume" {
			return nil, nil, fmt.Errorf("mount type %q at %s is not safely reconstructable", m.Type, m.Destination)
		}
		found := false
		for _, b := range in.HostConfig.Binds {
			if bindDestination(b) == m.Destination {
				found = true
				break
			}
		}
		if found {
			continue
		}
		source := m.Source
		if m.Type == "volume" && m.Name != "" {
			source = m.Name
		}
		spec := source + ":" + m.Destination
		if !m.RW {
			spec += ":ro"
		}
		a = append(a, "--volume", spec)
	}
	tmpfsDestinations := make([]string, 0, len(in.HostConfig.Tmpfs))
	for destination := range in.HostConfig.Tmpfs {
		tmpfsDestinations = append(tmpfsDestinations, destination)
	}
	sort.Strings(tmpfsDestinations)
	for _, destination := range tmpfsDestinations {
		spec := destination
		if options := in.HostConfig.Tmpfs[destination]; options != "" {
			spec += ":" + options
		}
		a = append(a, "--tmpfs", spec)
	}
	ports := make([]string, 0, len(in.HostConfig.PortBindings))
	for p := range in.HostConfig.PortBindings {
		ports = append(ports, p)
	}
	sort.Strings(ports)
	for _, p := range ports {
		for _, b := range in.HostConfig.PortBindings[p] {
			if b.HostPort == "" {
				return nil, nil, fmt.Errorf("port %s has no stable host port", p)
			}
			host := b.HostPort
			if b.HostIP != "" {
				host = b.HostIP + ":" + host
			}
			a = append(a, "--publish", host+":"+p)
		}
	}
	if rp := in.HostConfig.RestartPolicy.Name; rp != "" && rp != "no" {
		if rp == "on-failure" && in.HostConfig.RestartPolicy.MaximumRetryCount > 0 {
			rp += ":" + strconv.Itoa(in.HostConfig.RestartPolicy.MaximumRetryCount)
		}
		a = append(a, "--restart", rp)
	}
	if in.HostConfig.Privileged {
		a = append(a, "--privileged")
		warnings = append(warnings, "container is privileged")
	}
	if in.HostConfig.ReadonlyRootfs {
		a = append(a, "--read-only")
	}
	for _, c := range in.HostConfig.CapAdd {
		a = append(a, "--cap-add", c)
	}
	for _, c := range in.HostConfig.CapDrop {
		a = append(a, "--cap-drop", c)
	}
	if in.Config.WorkingDir != "" {
		a = append(a, "--workdir", in.Config.WorkingDir)
	}
	if in.Config.User != "" {
		a = append(a, "--user", in.Config.User)
	}
	primaryNetwork, additionalNetworks := networkPlan(in)
	if primaryNetwork != "" && primaryNetwork != "bridge" && primaryNetwork != "default" {
		a = append(a, "--network", primaryNetwork)
	}
	if len(additionalNetworks) > 0 {
		warnings = append(warnings, "additional networks will be connected after creation: "+strings.Join(additionalNetworks, ", "))
	}
	if len(in.Config.Entrypoint) > 0 {
		a = append(a, "--entrypoint", in.Config.Entrypoint[0])
	}
	a = append(a, in.Config.Image)
	if len(in.Config.Entrypoint) > 1 {
		a = append(a, in.Config.Entrypoint[1:]...)
	}
	a = append(a, in.Config.Cmd...)
	return a, warnings, nil
}

func bindDestination(spec string) string {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func networkPlan(in inspectContainer) (string, []string) {
	primary := in.HostConfig.NetworkMode
	names := make([]string, 0, len(in.NetworkSettings.Networks))
	for name := range in.NetworkSettings.Networks {
		if name != "bridge" && name != "default" && name != "host" && name != "none" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if primary == "default" || primary == "bridge" {
		return primary, names
	}
	if primary == "" {
		if len(names) == 0 {
			return primary, nil
		}
		primary = names[0]
		names = names[1:]
		return primary, names
	}
	if _, ok := in.NetworkSettings.Networks[primary]; ok {
		additional := names[:0]
		for _, name := range names {
			if name != primary {
				additional = append(additional, name)
			}
		}
		return primary, additional
	}
	if len(names) == 1 {
		return primary, nil
	}
	return primary, names
}
func redactArgs(args []string) []string {
	out := append([]string{}, args...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "--env" {
			key := strings.SplitN(out[i+1], "=", 2)[0]
			out[i+1] = key + "=<redacted>"
		}
	}
	return out
}

func (u Updater) executeRun(ctx context.Context, in inspectContainer, logf func(string, string)) error {
	name := strings.TrimPrefix(in.Name, "/")
	backup := name + ".dockpulse-backup"
	args, _, err := createArgs(in, name)
	if err != nil {
		return err
	}
	logf("info", "pulling "+in.Config.Image)
	if _, err = u.Docker.Run(ctx, "pull", in.Config.Image); err != nil {
		return err
	}
	logf("info", "stopping current container")
	if _, err = u.Docker.Run(ctx, "stop", name); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = u.Docker.Run(recoveryCtx, "start", name)
		return err
	}
	if _, err = u.Docker.Run(ctx, "rename", name, backup); err != nil {
		_, _ = u.Docker.Run(ctx, "start", name)
		return err
	}
	rollback := func(cause error) error {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = u.Docker.Run(rollbackCtx, "rm", "-f", name)
		_, renameErr := u.Docker.Run(rollbackCtx, "rename", backup, name)
		if renameErr != nil {
			return fmt.Errorf("%w; rollback rename failed: %v", cause, renameErr)
		}
		_, startErr := u.Docker.Run(rollbackCtx, "start", name)
		if startErr != nil {
			return fmt.Errorf("%w; rollback start failed: %v", cause, startErr)
		}
		return fmt.Errorf("%w; previous container restored", cause)
	}
	if _, err = u.Docker.Run(ctx, args...); err != nil {
		return rollback(err)
	}
	_, additional := networkPlan(in)
	for _, n := range additional {
		if _, err = u.Docker.Run(ctx, "network", "connect", n, name); err != nil {
			return rollback(err)
		}
	}
	if _, err = u.Docker.Run(ctx, "start", name); err != nil {
		return rollback(err)
	}
	raw, err := u.Docker.Run(ctx, "inspect", "--format", "{{.State.Running}}", name)
	if err != nil || strings.TrimSpace(string(raw)) != "true" {
		if err == nil {
			err = fmt.Errorf("replacement did not reach running state")
		}
		return rollback(err)
	}
	if _, err = u.Docker.Run(ctx, "rm", backup); err != nil {
		logf("warn", "replacement is running but rollback container could not be removed: "+err.Error())
		return nil
	}
	logf("info", "replacement running; rollback candidate removed")
	return nil
}
