package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dockpulse/dockpulse/internal/auth"
	"github.com/dockpulse/dockpulse/internal/domain"
)

func TestPinnedDigestQueriesPinnedManifest(t *testing.T) {
	for _, ref := range []string{"nginx@sha256:", "nginx:stable@sha256:"} {
		var path string
		r := &RegistryClient{Client: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			path = req.URL.Path
			return &http.Response{StatusCode: 200, Header: http.Header{"Docker-Content-Digest": []string{"sha256:new"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}}
		_, err := r.Digest(t.Context(), ref+strings.Repeat("a", 64))
		if err != nil || path != "/v2/library/nginx/manifests/sha256:"+strings.Repeat("a", 64) {
			t.Fatalf("regression: path=%q err=%v", path, err)
		}
	}
}

func TestMutationAdmissionIsIdempotentAndRejectsChangedRequests(t *testing.T) {
	a := New(Config{}, Discovery{}, Updater{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := domain.ActionRequest{ControlJobID: "job", ContainerID: "one", Confirm: true}
	id, fresh, err := a.admitMutation("job", "update", req, func() error { return nil })
	if err != nil || !fresh {
		t.Fatalf("first admission: %v", err)
	}
	again, fresh, err := a.admitMutation("job", "update", req, func() error { t.Fatal("duplicate was revalidated"); return nil })
	if err != nil || fresh || again != id {
		t.Fatalf("duplicate started another job: %q %v %v", again, fresh, err)
	}
	req.ContainerID = "two"
	if _, _, err := a.admitMutation("job", "update", req, func() error { return nil }); err == nil {
		t.Fatal("changed request reused job ID")
	}
	a.releaseMutation(id)
	if _, fresh, err := a.admitMutation("next", "update", req, func() error { return nil }); err != nil || !fresh {
		t.Fatalf("lock not released: %v", err)
	}
}

// This uses only Compose config inspection, never the Docker daemon.
func TestComposeInterpolationContextMatchesDeployment(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker CLI unavailable")
	}
	d := DockerCLI{}
	if _, err := d.Run(t.Context(), "compose", "version"); err != nil {
		t.Skip("Compose CLI unavailable")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yml")
	envFile := filepath.Join(dir, "custom.env")
	if err := os.WriteFile(file, []byte("services:\n  app:\n    image: ${APP_IMAGE:-alpine:3.22}\n    profiles: [maintenance]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, []byte("APP_IMAGE=busybox:1.37\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var in inspectContainer
	in.Config.Labels = map[string]string{"com.docker.compose.project": "review", "com.docker.compose.service": "app", "com.docker.compose.project.working_dir": dir, "com.docker.compose.project.config_files": file, "com.docker.compose.project.environment_file": envFile}
	raw, err := d.Run(t.Context(), append(composeArgs(in), "--profile", "*", "config", "--hash", "app")...)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		t.Fatalf("invalid hash output %q", raw)
	}
	in.Config.Labels["com.docker.compose.config-hash"] = fields[1]
	u := Updater{Docker: d, AllowedComposeRoots: []string{dir}}
	if _, err := u.composePlan(t.Context(), in, "service", nil); err != nil {
		t.Fatalf("original context rejected: %v", err)
	}
	delete(in.Config.Labels, "com.docker.compose.project.environment_file")
	if _, err := u.composePlan(t.Context(), in, "service", nil); err == nil {
		t.Fatal("lost environment file accepted")
	}
	in.Config.Labels["com.docker.compose.project.environment_file"] = envFile
	if err := os.WriteFile(envFile, []byte("APP_IMAGE=alpine:3.22\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := u.composePlan(t.Context(), in, "service", nil); err == nil {
		t.Fatal("changed interpolation input accepted")
	}
	delete(in.Config.Labels, "com.docker.compose.config-hash")
	if _, err := u.composePlan(t.Context(), in, "service", nil); err == nil {
		t.Fatal("unverifiable deployment accepted")
	}
}

func TestAgentRejectsOverlappingMutations(t *testing.T) {
	var in inspectContainer
	in.ID = "container-id"
	in.Name = "/app"
	in.Config.Image = "alpine:3.22"
	in.Config.Labels = map[string]string{"io.dockpulse.manage": "true"}
	raw, _ := json.Marshal([]inspectContainer{in})
	entered := make(chan string, 4)
	release := make(chan struct{})
	runner := runFunc(func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "inspect" && len(args) == 2 {
			return raw, nil
		}
		if args[0] == "pull" || (args[0] == "compose" && strings.Contains(strings.Join(args, " "), " pull ")) {
			entered <- args[0]
			<-release
		}
		if args[0] == "inspect" {
			return []byte("true"), nil
		}
		return nil, nil
	})
	secret := "abcdefghijklmnopqrstuvwxyz123456"
	a := New(Config{ID: "a", Secret: secret, ControlURL: "http://control"}, Discovery{Docker: runner}, Updater{Docker: runner, SelfUpdate: SelfUpdateConfig{Enabled: true, Directory: "/app", Project: "agent", Service: "agent"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})}
	post := func(path string, input any) int {
		body, _ := json.Marshal(input)
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		auth.Sign(req, "a", []byte(secret), body, time.Now())
		res := httptest.NewRecorder()
		a.Handler().ServeHTTP(res, req)
		return res.Code
	}
	// Keep the first workload active while trying a second mutation and self-update.
	if code := post("/update", domain.ActionRequest{ControlJobID: "first", ContainerID: in.ID, Confirm: true}); code != 202 {
		t.Fatalf("first: %d", code)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("first executor did not start")
	}
	second := post("/update", domain.ActionRequest{ControlJobID: "second", ContainerID: in.ID, Confirm: true})
	self := post("/self-update", domain.SelfUpdateRequest{ControlJobID: "self", Confirm: true, TargetVersion: "0.3.6"})
	if code := post("/update", domain.ActionRequest{ControlJobID: "first", ContainerID: in.ID, Confirm: true}); code != 202 {
		close(release)
		t.Fatalf("duplicate request rejected: %d", code)
	}
	close(release)
	// Wait for background jobs so the test does not outlive its executors.
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.RLock()
		done := len(a.jobs) == 1
		for _, j := range a.jobs {
			if j.Job.Status != domain.StatusSucceeded && j.Job.Status != domain.StatusFailed {
				done = false
			}
		}
		a.mu.RUnlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("executors did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if second != 409 || self != 409 {
		t.Fatalf("regression: second=%d self=%d", second, self)
	}
}
