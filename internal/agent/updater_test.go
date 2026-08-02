package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateArgsPreservesContractAndRedactsPlan(t *testing.T) {
	var in inspectContainer
	in.Name = "/api"
	in.Config.Image = "example/api:1"
	in.Config.Env = []string{"TOKEN=secret", "MODE=prod"}
	in.Config.Labels = map[string]string{"io.dockpulse.manage": "true"}
	in.Config.Cmd = []string{"serve"}
	in.HostConfig.Binds = []string{"data:/data"}
	in.HostConfig.RestartPolicy.Name = "unless-stopped"
	in.HostConfig.PortBindings = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{"8080/tcp": {{HostIP: "127.0.0.1", HostPort: "8080"}}}
	args, _, err := createArgs(in, "api")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--env TOKEN=secret", "--volume data:/data", "--publish 127.0.0.1:8080:8080/tcp", "--restart unless-stopped", "example/api:1 serve"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	redacted := strings.Join(redactArgs(args), " ")
	if strings.Contains(redacted, "secret") {
		t.Fatal("dry-run leaked environment secret")
	}
}

func TestCreateArgsRejectsUnsafeReconstruction(t *testing.T) {
	var in inspectContainer
	in.Name = "/ephemeral"
	in.Config.Image = "example/app:1"
	in.Config.Labels = map[string]string{"io.dockpulse.manage": "true"}
	in.HostConfig.AutoRemove = true
	if _, _, err := createArgs(in, "ephemeral"); err == nil || !strings.Contains(err.Error(), "--rm") {
		t.Fatalf("expected an explicit auto-remove rejection, got %v", err)
	}
}

func TestCreateArgsMatchesBindDestinationsExactly(t *testing.T) {
	var in inspectContainer
	in.Config.Image = "example/app:1"
	in.HostConfig.Binds = []string{"host:/data2"}
	in.Mounts = append(in.Mounts, struct {
		Type, Source, Destination, Name string
		RW                              bool
	}{Type: "volume", Name: "data", Destination: "/data", RW: true})
	args, _, err := createArgs(in, "app")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--volume data:/data") {
		t.Fatalf("named volume was lost due to a prefix match: %s", joined)
	}
}

func TestNetworkPlanPreservesBridgeAsPrimary(t *testing.T) {
	var in inspectContainer
	in.HostConfig.NetworkMode = "bridge"
	in.NetworkSettings.Networks = map[string]json.RawMessage{"bridge": nil, "metrics": nil}
	primary, additional := networkPlan(in)
	if primary != "bridge" || len(additional) != 1 || additional[0] != "metrics" {
		t.Fatalf("unexpected network plan: primary=%q additional=%v", primary, additional)
	}
}

func TestDockerCLIErrorRedactsEnvironment(t *testing.T) {
	_, err := (DockerCLI{Binary: "false"}).Run(context.Background(), "create", "--env", "TOKEN=top-secret", "image")
	if err == nil {
		t.Fatal("expected command failure")
	}
	if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("command error leaked an environment value: %v", err)
	}
}
