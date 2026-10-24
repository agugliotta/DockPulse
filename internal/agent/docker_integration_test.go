package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dockpulse/dockpulse/internal/domain"
)

func integrationDocker(t *testing.T) (DockerCLI, string) {
	t.Helper()
	if os.Getenv("DOCKPULSE_DOCKER_TESTS") != "1" {
		t.Skip("set DOCKPULSE_DOCKER_TESTS=1 to run isolated Docker integration tests")
	}
	d := DockerCLI{}
	if _, err := d.Run(t.Context(), "version"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Run(t.Context(), "image", "inspect", "alpine:3.22"); err != nil {
		if _, err := d.Run(t.Context(), "pull", "alpine:3.22"); err != nil {
			t.Fatal(err)
		}
	}
	name := fmt.Sprintf("dockpulse-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = d.Run(ctx, "rm", "-f", name, name+".dockpulse-backup", name+"-copy")
	})
	return d, name
}

func TestDockerIntegrationRunUpdateAndRollback(t *testing.T) {
	d, name := integrationDocker(t)
	if _, err := d.Run(t.Context(), "run", "-d", "--name", name, "--network", "none", "--stop-timeout", "1", "--label", "io.dockpulse.manage=true", "--shm-size", "96m", "--log-driver", "json-file", "--log-opt", "max-size=2m", "--log-opt", "max-file=2", "alpine:3.22", "sleep", "600"); err != nil {
		t.Fatal(err)
	}
	u := Updater{Docker: d}
	original, err := u.inspect(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	if !toContainer("test", original, nil).Manageable {
		t.Fatal("supported daemon settings incorrectly block management")
	}
	req := domain.ActionRequest{ContainerID: original.ID, Scope: "container", Confirm: true}
	if err := u.Execute(t.Context(), req, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	replacement, err := u.inspect(t.Context(), name)
	if err != nil || replacement.ID == original.ID || replacement.State.Status != "running" {
		t.Fatalf("replacement failed: %s %v", replacement.ID, err)
	}
	if replacement.HostConfig.ShmSize != original.HostConfig.ShmSize || !reflect.DeepEqual(replacement.HostConfig.LogConfig, original.HostConfig.LogConfig) {
		t.Fatalf("shm or logging settings changed: %+v", replacement.HostConfig)
	}
	failed := false
	u.Docker = runFunc(func(ctx context.Context, args ...string) ([]byte, error) {
		if !failed && len(args) == 2 && args[0] == "start" && args[1] == name {
			failed = true
			return nil, fmt.Errorf("intentional integration-test start failure")
		}
		return d.Run(ctx, args...)
	})
	req.ContainerID = replacement.ID
	if err := u.Execute(t.Context(), req, func(string, string) {}); err == nil || !strings.Contains(err.Error(), "previous container restored") {
		t.Fatalf("rollback failed: %v", err)
	}
	restored, err := u.inspect(t.Context(), name)
	if err != nil || restored.ID != replacement.ID || restored.State.Status != "running" {
		t.Fatalf("previous running container not restored: %s %v", restored.ID, err)
	}
}

func TestDockerIntegrationMountPropagation(t *testing.T) {
	d, name := integrationDocker(t)
	source := t.TempDir()
	if _, err := d.Run(t.Context(), "create", "--name", name, "--mount", "type=bind,source="+source+",target=/data,readonly,bind-propagation=rshared", "alpine:3.22", "sleep", "600"); err != nil {
		t.Fatal(err)
	}
	u := Updater{Docker: d}
	original, err := u.inspect(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	args, _, err := createArgs(original, name+"-copy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Run(t.Context(), args...); err != nil {
		t.Fatal(err)
	}
	copy, err := u.inspect(t.Context(), name+"-copy")
	if err != nil || len(copy.Mounts) != 1 {
		t.Fatalf("missing mount: %+v %v", copy.Mounts, err)
	}
	if copy.Mounts[0].Propagation != "rshared" || copy.Mounts[0].RW {
		t.Fatalf("mount settings changed: %+v", copy.Mounts[0])
	}
}

func TestDockerIntegrationProfiledService(t *testing.T) {
	d, project := integrationDocker(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yml")
	config := "services:\n  web:\n    image: alpine:3.22\n    command: [sleep, '600']\n    stop_grace_period: 1s\n  tool:\n    image: alpine:3.22\n    command: [sleep, '%s']\n    stop_grace_period: 1s\n    profiles: [maintenance]\n"
	if err := os.WriteFile(file, []byte(fmt.Sprintf(config, "600")), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := d.Run(ctx, "compose", "-p", project, "-f", file, "--profile", "maintenance", "down", "--timeout", "1"); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Run(t.Context(), "compose", "-p", project, "-f", file, "up", "-d", "web", "tool"); err != nil {
		t.Fatal(err)
	}
	u := Updater{Docker: d, AllowedComposeRoots: []string{dir}}
	original, err := u.inspect(t.Context(), project+"-tool-1")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := u.inspect(t.Context(), project+"-web-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(fmt.Sprintf(config, "601")), 0600); err != nil {
		t.Fatal(err)
	}
	req := domain.ActionRequest{ContainerID: original.ID, Scope: "service", Confirm: true}
	if _, err := u.DryRun(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := u.Execute(t.Context(), req, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	after, err := u.inspect(t.Context(), project+"-tool-1")
	if err != nil || after.ID == original.ID || after.State.Status != "running" {
		t.Fatalf("profiled service was not recreated: %s %v", after.ID, err)
	}
	untouched, err := u.inspect(t.Context(), project+"-web-1")
	if err != nil || untouched.ID != sibling.ID || untouched.State.Status != "running" {
		t.Fatalf("sibling was changed: %s %v", untouched.ID, err)
	}
}
