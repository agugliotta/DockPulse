package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dockpulse/dockpulse/internal/domain"
)

type Runner interface {
	Run(context.Context, ...string) ([]byte, error)
}

type EnvRunner interface {
	Runner
	RunWithEnv(context.Context, []string, ...string) ([]byte, error)
}

type DockerCLI struct{ Binary string }

func (d DockerCLI) Run(ctx context.Context, args ...string) ([]byte, error) {
	return d.RunWithEnv(ctx, nil, args...)
}

func (d DockerCLI) RunWithEnv(ctx context.Context, env []string, args ...string) ([]byte, error) {
	bin := d.Binary
	if bin == "" {
		bin = "docker"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", strings.Join(redactArgs(args), " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type inspectContainer struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Image string `json:"Image"`
	State struct {
		Status string `json:"Status"`
	} `json:"State"`
	Config struct {
		Image      string            `json:"Image"`
		Labels     map[string]string `json:"Labels"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		WorkingDir string            `json:"WorkingDir"`
		User       string            `json:"User"`
	} `json:"Config"`
	HostConfig struct {
		Binds         []string `json:"Binds"`
		NetworkMode   string   `json:"NetworkMode"`
		RestartPolicy struct {
			Name              string `json:"Name"`
			MaximumRetryCount int    `json:"MaximumRetryCount"`
		} `json:"RestartPolicy"`
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
		Privileged      bool `json:"Privileged"`
		CapAdd, CapDrop []string
		ReadonlyRootfs  bool              `json:"ReadonlyRootfs"`
		AutoRemove      bool              `json:"AutoRemove"`
		PublishAllPorts bool              `json:"PublishAllPorts"`
		Tmpfs           map[string]string `json:"Tmpfs"`
		Links           []string          `json:"Links"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type, Source, Destination, Name string
		RW                              bool
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

type imageInspect struct {
	RepoDigests []string `json:"RepoDigests"`
}

type Discovery struct {
	Docker              Runner
	Registry            *RegistryClient
	AllowedComposeRoots []string
}

func (d Discovery) Inventory(ctx context.Context, agentID string, checkUpdates bool) (domain.Inventory, error) {
	idsRaw, err := d.Docker.Run(ctx, "ps", "-aq")
	if err != nil {
		return domain.Inventory{}, err
	}
	ids := strings.Fields(string(idsRaw))
	inv := domain.Inventory{AgentID: agentID, SyncedAt: time.Now().UTC(), Containers: []domain.Container{}}
	if len(ids) == 0 {
		return inv, nil
	}
	args := append([]string{"inspect"}, ids...)
	raw, err := d.Docker.Run(ctx, args...)
	if err != nil {
		return inv, err
	}
	var inspected []inspectContainer
	if err = json.Unmarshal(raw, &inspected); err != nil {
		return inv, fmt.Errorf("decode docker inspect: %w", err)
	}
	for _, in := range inspected {
		c := toContainer(agentID, in, d.AllowedComposeRoots)
		imgRaw, e := d.Docker.Run(ctx, "image", "inspect", in.Image)
		if e == nil {
			var ii []imageInspect
			if json.Unmarshal(imgRaw, &ii) == nil && len(ii) > 0 {
				c.CurrentDigest = matchingDigest(in.Config.Image, ii[0].RepoDigests)
			}
		}
		if checkUpdates && d.Registry != nil {
			remote, e := d.Registry.Digest(ctx, in.Config.Image)
			if e == nil && remote != "" {
				c.RemoteDigest = remote
				c.DetectionMethod = "registry-manifest-digest"
				c.UpdateAvailable = c.CurrentDigest != "" && normalizeDigest(c.CurrentDigest) != normalizeDigest(remote)
			} else if e != nil {
				c.SafetyReason = appendReason(c.SafetyReason, "update check: "+e.Error())
			}
		}
		inv.Containers = append(inv.Containers, c)
	}
	sort.Slice(inv.Containers, func(i, j int) bool { return inv.Containers[i].Name < inv.Containers[j].Name })
	return inv, nil
}

func toContainer(agentID string, in inspectContainer, allowedComposeRoots []string) domain.Container {
	labels := in.Config.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	kind := "docker-run"
	project := labels["com.docker.compose.project"]
	service := labels["com.docker.compose.service"]
	workdir := labels["com.docker.compose.project.working_dir"]
	if project != "" {
		kind = "compose"
	}
	tag := imageTag(in.Config.Image)
	sensitive := isSensitive(in.Name, in.Config.Image, labels)
	manageable := labels["io.dockpulse.manage"] == "true"
	reason := ""
	if kind == "compose" {
		if err := validateComposeDir(workdir, allowedComposeRoots); err != nil {
			manageable = false
			reason = err.Error()
		} else {
			manageable = true
		}
	} else if !manageable {
		reason = "docker-run containers require label io.dockpulse.manage=true"
	}
	if labels["io.dockpulse.manage"] == "false" {
		manageable = false
		reason = "disabled by io.dockpulse.manage=false"
	}
	if sensitive && labels["io.dockpulse.allow-sensitive"] != "true" {
		manageable = false
		reason = appendReason(reason, "sensitive workload requires io.dockpulse.allow-sensitive=true")
	}
	return domain.Container{ID: agentID + ":" + in.ID, DockerID: in.ID, AgentID: agentID, Name: strings.TrimPrefix(in.Name, "/"), Image: in.Config.Image, Tag: tag, RuntimeStatus: in.State.Status, ManagementKind: kind, ComposeProject: project, ComposeService: service, ComposeWorkingDir: workdir, Labels: labels, Sensitive: sensitive, Manageable: manageable, SafetyReason: reason, LastSync: time.Now().UTC()}
}

func validateComposeDir(dir string, roots []string) error {
	if dir == "" {
		return fmt.Errorf("compose working directory is not available from container labels")
	}
	clean, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("invalid Compose directory %q", dir)
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return fmt.Errorf("Compose directory %q is unavailable to the agent", clean)
	}
	allowed := false
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root, rootErr := filepath.Abs(filepath.Clean(root))
		if rootErr != nil || root == "." {
			continue
		}
		root, rootErr = filepath.EvalSymlinks(root)
		if rootErr == nil && (resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator))) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("Compose directory %q is outside configured allow-list", clean)
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("Compose directory %q is unavailable to the agent", clean)
	}
	return nil
}
func imageTag(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[colon+1:]
	}
	return "latest"
}
func matchingDigest(ref string, digests []string) string {
	repo, _, _ := parseReference(ref)
	for _, v := range digests {
		parts := strings.SplitN(v, "@", 2)
		if len(parts) == 2 && (parts[0] == repo || strings.HasSuffix(repo, "/"+parts[0]) || strings.HasSuffix(parts[0], "/"+repo)) {
			return parts[1]
		}
	}
	if len(digests) > 0 {
		p := strings.SplitN(digests[0], "@", 2)
		if len(p) == 2 {
			return p[1]
		}
	}
	return ""
}
func normalizeDigest(v string) string {
	if i := strings.LastIndex(v, "@"); i >= 0 {
		return v[i+1:]
	}
	return v
}
func appendReason(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
func isSensitive(name, image string, labels map[string]string) bool {
	if labels["io.dockpulse.sensitive"] == "true" {
		return true
	}
	v := strings.ToLower(name + " " + image)
	for _, word := range []string{"postgres", "mysql", "mariadb", "mongo", "redis", "valkey", "etcd", "database", "db"} {
		for _, token := range strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == ':' || r == '-' || r == '_' || r == '.' || r == ' ' }) {
			if token == word {
				return true
			}
		}
	}
	return false
}
