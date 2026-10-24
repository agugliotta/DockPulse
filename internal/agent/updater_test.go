package agent

import (
	"context"
	"encoding/json"
	"reflect"
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
	in.Config.Hostname = "api-host"
	in.Config.Domainname = "internal"
	in.Config.StopSignal = "SIGQUIT"
	timeout := 30
	in.Config.StopTimeout = &timeout
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
	for _, want := range []string{"--env TOKEN=secret", "--volume data:/data", "--publish 127.0.0.1:8080:8080/tcp", "--restart unless-stopped", "--hostname api-host", "--domainname internal", "--stop-signal SIGQUIT", "--stop-timeout 30", "example/api:1 serve"} {
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
		Type, Source, Destination, Name, Propagation string
		RW                                           bool
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

type recordingRunner struct {
	envs [][]string
	args [][]string
}

func (r *recordingRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	r.args = append(r.args, args)
	return []byte(""), nil
}

func (r *recordingRunner) RunWithEnv(ctx context.Context, env []string, args ...string) ([]byte, error) {
	r.envs = append(r.envs, append([]string{}, env...))
	return r.Run(ctx, args...)
}

func TestSelfUpdateInjectsRequestedVersion(t *testing.T) {
	runner := &recordingRunner{}
	u := Updater{Docker: runner, SelfUpdate: SelfUpdateConfig{Enabled: true, Directory: "/opt/dockpulse", Project: "dockpulse-agent", Service: "agent"}}
	if err := u.ExecuteSelfUpdate(context.Background(), "latest", func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	wantEnv := []string{"DOCKPULSE_VERSION=latest", "DOCKPULSE_UPDATE_VERSION=latest"}
	if len(runner.envs) != 2 || !reflect.DeepEqual(runner.envs[0], wantEnv) || !reflect.DeepEqual(runner.envs[1], wantEnv) {
		t.Fatalf("self-update did not inject target version: %+v", runner.envs)
	}
}

func TestCreateArgsPreservesDaemonSettings(t *testing.T) {
	for _, driver := range []string{"json-file", "local", "splunk"} {
		t.Run(driver, func(t *testing.T) {
			var in inspectContainer
			in.Config.Image = "alpine:3.22"
			in.HostConfig.ShmSize = 4199546880
			in.HostConfig.LogConfig.Type = driver
			in.HostConfig.LogConfig.Config = map[string]string{"max-size": "20m", "token": "secret"}
			args, _, err := createArgs(in, "app")
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			for _, want := range []string{"--shm-size 4199546880", "--log-driver " + driver, "--log-opt max-size=20m", "--log-opt token=secret"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing %q in %s", want, joined)
				}
			}
			if strings.Contains(strings.Join(redactArgs(args), " "), "secret") {
				t.Fatal("logging secret exposed by preview")
			}
		})
	}
}

func TestStructuredBindMountPreservesPropagationAndReadOnly(t *testing.T) {
	var in inspectContainer
	if err := json.Unmarshal([]byte(`{"Config":{"Image":"alpine"},"HostConfig":{"Mounts":[{"Type":"bind","Source":"/mnt","Target":"/data","ReadOnly":true,"BindOptions":{"Propagation":"rshared"}}]},"Mounts":[{"Type":"bind","Source":"/mnt","Destination":"/data","RW":false,"Propagation":"rshared"}]}`), &in); err != nil {
		t.Fatal(err)
	}
	args, _, err := createArgs(in, "app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--volume /mnt:/data:ro,rshared") {
		t.Fatalf("mount settings lost: %v", args)
	}
}

func TestUnsupportedStructuredMountOptionsBlockRecreation(t *testing.T) {
	for _, mount := range []string{
		`{"Type":"bind","BindOptions":{"NonRecursive":true}}`,
		`{"Type":"volume","VolumeOptions":{"NoCopy":true}}`,
		`{"Type":"volume","VolumeOptions":{"Subpath":"data"}}`,
		`{"Type":"tmpfs"}`,
	} {
		var in inspectContainer
		if err := json.Unmarshal([]byte(`{"HostConfig":{"Mounts":[`+mount+`]}}`), &in); err != nil {
			t.Fatal(err)
		}
		if _, _, err := createArgs(in, "app"); err == nil {
			t.Fatalf("unsupported mount accepted: %s", mount)
		}
	}
}
