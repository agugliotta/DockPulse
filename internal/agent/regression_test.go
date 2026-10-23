package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type runFunc func(context.Context, ...string) ([]byte, error)

func (f runFunc) Run(ctx context.Context, args ...string) ([]byte, error) { return f(ctx, args...) }

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRejectsUnsupportedContainerSettings(t *testing.T) {
	for _, raw := range []string{
		`{"HostConfig":{"Memory":536870912}}`,
		`{"HostConfig":{"Devices":[{"PathOnHost":"/dev/video0"}]}}`,
		`{"HostConfig":{"SecurityOpt":["no-new-privileges"]}}`,
		`{"HostConfig":{"NanoCpus":1000000000}}`,
		`{"HostConfig":{"PidsLimit":100}}`,
		`{"HostConfig":{"OomKillDisable":true}}`,
		`{"HostConfig":{"Init":true}}`,
		`{"Config":{"Tty":true}}`,
		`{"Config":{"Healthcheck":{"Test":["CMD","true"]}}}`,
		`{"NetworkSettings":{"Networks":{"app":{"Aliases":["custom"]}}}}`,
		`{"NetworkSettings":{"Networks":{"app":{"IPAMConfig":{"IPv4Address":"172.20.0.2"}}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var in inspectContainer
			if err := json.Unmarshal([]byte(raw), &in); err != nil {
				t.Fatal(err)
			}
			in.Config.Labels = map[string]string{"io.dockpulse.manage": "true"}
			if _, _, err := createArgs(in, "app"); err == nil {
				t.Fatal("unsupported contract was accepted")
			}
			if c := toContainer("a", in, nil); c.Manageable {
				t.Fatal("inventory marked unsupported contract manageable")
			}
		})
	}
}

func TestComposeStackValidatesEveryService(t *testing.T) {
	dir := t.TempDir()
	for _, labels := range []string{`{"io.dockpulse.manage":"false"}`, `{}`} {
		in := inspectContainer{}
		in.Config.Labels = map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "web", "com.docker.compose.project.working_dir": dir}
		config := `{"services":{"web":{"image":"nginx"},"db":{"image":"postgres:16","labels":` + labels + `}}}`
		u := Updater{AllowedComposeRoots: []string{dir}, Docker: runFunc(func(_ context.Context, args ...string) ([]byte, error) { return []byte(config), nil })}
		if _, err := u.composePlan(t.Context(), in, "stack"); err == nil {
			t.Fatal("stack accepted disallowed database")
		}
	}
}

func TestServiceUpdateDoesNotRecreateDependencies(t *testing.T) {
	in := inspectContainer{}
	in.Config.Labels = map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "web", "com.docker.compose.project.working_dir": "/app", "com.docker.compose.project.config_files": "/app/base.yml,/app/prod.yml"}
	r := &recordingRunner{}
	if err := (Updater{Docker: r}).executeCompose(t.Context(), in, "service"); err != nil {
		t.Fatal(err)
	}
	up := strings.Join(r.args[1], " ")
	if !strings.Contains(up, "up -d --no-deps web") || !strings.Contains(up, "-f /app/base.yml -f /app/prod.yml") {
		t.Fatalf("unsafe service command: %s", up)
	}
}

func TestComposeServiceValidatesItsCurrentConfiguration(t *testing.T) {
	dir := t.TempDir()
	in := inspectContainer{}
	in.Config.Labels = map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "web", "com.docker.compose.project.working_dir": dir}
	for _, tc := range []struct {
		config  string
		allowed bool
	}{
		{`{"services":{"web":{"image":"nginx"},"db":{"image":"postgres"}}}`, true},
		{`{"services":{"web":{"image":"nginx","labels":{"io.dockpulse.manage":"false"}}}}`, false},
		{`{"services":{"web":{"image":"postgres"}}}`, false},
		{`{"services":{"other":{"image":"nginx"}}}`, false},
	} {
		u := Updater{AllowedComposeRoots: []string{dir}, Docker: runFunc(func(context.Context, ...string) ([]byte, error) { return []byte(tc.config), nil })}
		_, err := u.composePlan(t.Context(), in, "service")
		if (err == nil) != tc.allowed {
			t.Fatalf("config %s: %v", tc.config, err)
		}
	}
}

func TestSelfUpdateUsesDeploymentFiles(t *testing.T) {
	for _, custom := range []bool{false, true} {
		r := &recordingRunner{}
		cfg := SelfUpdateConfig{Enabled: true, Directory: "/opt/dockpulse", Project: "dockpulse-agent", Service: "agent"}
		file, envFile := "/opt/dockpulse/deploy/docker-compose.agent.yml", "/opt/dockpulse/deploy/agent.env"
		if custom {
			file, envFile = "/srv/custom.yml", "/srv/custom.env"
			cfg.ComposeFile, cfg.EnvFile = file, envFile
		}
		if err := (Updater{Docker: r, SelfUpdate: cfg}).ExecuteSelfUpdate(t.Context(), "0.3.5", func(string, string) {}); err != nil {
			t.Fatal(err)
		}
		for i, args := range r.args {
			want := []string{"compose", "--project-directory", cfg.Directory, "--env-file", envFile, "-f", file, "-p", cfg.Project}
			if !reflect.DeepEqual(args[:len(want)], want) {
				t.Fatalf("command %d ignores deployment files: %v", i, args)
			}
		}
	}
}

func TestRunUpdateRollback(t *testing.T) {
	for _, fail := range []string{"create", "network connect", "start app", "inspect --format", "rename app app.dockpulse-backup"} {
		t.Run(fail, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls []string
			failed := false
			in := inspectContainer{}
			in.Name = "/app"
			in.Config.Image = "image:latest"
			in.HostConfig.NetworkMode = "bridge"
			in.NetworkSettings.Networks = map[string]json.RawMessage{"bridge": nil, "extra": nil}
			r := runFunc(func(c context.Context, args ...string) ([]byte, error) {
				command := strings.Join(args, " ")
				calls = append(calls, command)
				if !failed && strings.HasPrefix(command, fail) {
					failed = true
					cancel()
					return nil, fmt.Errorf("injected failure")
				}
				if err := c.Err(); err != nil {
					t.Fatalf("recovery reused cancelled context: %s", command)
				}
				return []byte("true"), nil
			})
			err := (Updater{Docker: r}).executeRun(ctx, in, func(string, string) {})
			if err == nil || !failed {
				t.Fatalf("missing failure: %v %v", calls, err)
			}
			if calls[len(calls)-1] != "start app" {
				t.Fatalf("original not restarted: %v", calls)
			}
			if fail != "rename app app.dockpulse-backup" && !strings.Contains(strings.Join(calls, "\n"), "rename app.dockpulse-backup app") {
				t.Fatalf("original name not restored: %v", calls)
			}
		})
	}
}

func TestRollbackReportsRecoveryFailure(t *testing.T) {
	for _, failure := range []string{"rename app.dockpulse-backup app", "start app"} {
		in := inspectContainer{}
		in.Name = "/app"
		in.Config.Image = "image"
		r := runFunc(func(_ context.Context, args ...string) ([]byte, error) {
			command := strings.Join(args, " ")
			if args[0] == "create" || command == failure {
				return nil, fmt.Errorf("injected failure")
			}
			return nil, nil
		})
		err := (Updater{Docker: r}).executeRun(t.Context(), in, func(string, string) {})
		if err == nil || !strings.Contains(err.Error(), "rollback") {
			t.Fatalf("lost recovery failure: %v", err)
		}
	}
}

func TestHeartbeatContinuesDuringInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	done := make(chan struct{})
	docker := runFunc(func(ctx context.Context, args ...string) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	a := New(Config{ID: "a", ControlURL: "http://control", HeartbeatInterval: 5 * time.Millisecond}, Discovery{Docker: docker}, Updater{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var heartbeats atomic.Int32
	a.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/heartbeat") {
			heartbeats.Add(1)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	go func() { a.RunControlLoop(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("inventory did not start")
	}
	deadline := time.After(time.Second)
	for heartbeats.Load() < 3 {
		select {
		case <-deadline:
			t.Fatal("inventory blocked heartbeats")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inventory worker did not stop")
	}
}

func TestInventoryReusesRegistryResults(t *testing.T) {
	var calls int
	registry := &RegistryClient{Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Docker-Content-Digest": []string{"sha256:new"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	f := &fakeRunner{responses: map[string]string{
		"ps -aq":            "a b",
		"inspect a b":       `[{"Id":"a","Name":"/a","Image":"img","Config":{"Image":"nginx:latest"}},{"Id":"b","Name":"/b","Image":"img","Config":{"Image":"nginx:latest"}}]`,
		"image inspect img": `[{"RepoDigests":["nginx@sha256:old"]}]`,
	}}
	inv, err := (Discovery{Docker: f, Registry: registry}).Inventory(t.Context(), "agent", true)
	if err != nil || len(inv.Containers) != 2 || calls != 1 {
		t.Fatalf("registry requests not reused: %d %+v %v", calls, inv, err)
	}
}
