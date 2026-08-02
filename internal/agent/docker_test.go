package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	responses map[string]string
	calls     []string
}

func TestValidateComposeDirRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escaped")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := validateComposeDir(link, []string{root}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	v, ok := f.responses[key]
	if !ok {
		return nil, fmt.Errorf("unexpected call %s", key)
	}
	return []byte(v), nil
}

func TestDiscoveryClassifiesComposeAndSensitiveWorkloads(t *testing.T) {
	f := &fakeRunner{responses: map[string]string{"ps -aq": "abc\n", "inspect abc": `[{"Id":"abc","Name":"/db","Image":"imgid","State":{"Status":"running"},"Config":{"Image":"postgres:16","Labels":{"com.docker.compose.project":"core","com.docker.compose.service":"db","com.docker.compose.project.working_dir":"/opt/stacks/core"}},"HostConfig":{},"Mounts":[],"NetworkSettings":{"Networks":{}}}]`, "image inspect imgid": `[{"RepoDigests":["postgres@sha256:aaa"]}]`}}
	inv, err := (Discovery{Docker: f}).Inventory(context.Background(), "lxc-101", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Containers) != 1 {
		t.Fatalf("got %d containers", len(inv.Containers))
	}
	c := inv.Containers[0]
	if c.ManagementKind != "compose" || !c.Sensitive || c.Manageable {
		t.Fatalf("unexpected classification: %+v", c)
	}
	if !strings.Contains(c.SafetyReason, "sensitive workload") {
		t.Fatalf("missing safety reason: %s", c.SafetyReason)
	}
}

func TestDockerRunRequiresExplicitManagementLabel(t *testing.T) {
	in := inspectContainer{}
	in.ID = "abc"
	in.Name = "/web"
	in.Config.Image = "nginx:1.27"
	in.Config.Labels = map[string]string{}
	c := toContainer("a", in, nil)
	if c.Manageable {
		t.Fatal("unlabelled docker-run container must not be manageable")
	}
	in.Config.Labels["io.dockpulse.manage"] = "true"
	c = toContainer("a", in, nil)
	if !c.Manageable {
		t.Fatalf("opted-in container should be manageable: %s", c.SafetyReason)
	}
}
