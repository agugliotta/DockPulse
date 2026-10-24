package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dockpulse/dockpulse/internal/auth"
	"github.com/dockpulse/dockpulse/internal/domain"
	"github.com/dockpulse/dockpulse/internal/store"
)

func TestHealthReportsRunningVersion(t *testing.T) {
	s := New(nil, "", "", slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.5"})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	var health map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || health["version"] != "0.3.5" || health["status"] != "ok" {
		t.Fatalf("unexpected health response: %d %v", w.Code, health)
	}
}

func TestStackUpdateChecksEveryMember(t *testing.T) {
	for _, policy := range []string{"ignored", "protected", "sensitive", "unmanageable"} {
		t.Run(policy, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "stack.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			ctx := t.Context()
			if err := st.RegisterAgent(ctx, domain.Agent{ID: "a", Name: "a", BaseURL: "http://agent"}, []byte("secret")); err != nil {
				t.Fatal(err)
			}
			members := []domain.Container{
				{DockerID: "web", Name: "web", RuntimeStatus: "running", ManagementKind: "compose", ComposeProject: "app", ComposeWorkingDir: "/app", Manageable: true},
				{DockerID: "db", Name: "db", RuntimeStatus: "running", ManagementKind: "compose", ComposeProject: "app", Manageable: policy != "unmanageable", Sensitive: policy == "sensitive"},
			}
			if err := st.UpsertInventory(ctx, domain.Inventory{AgentID: "a", SyncedAt: time.Now(), Containers: members}); err != nil {
				t.Fatal(err)
			}
			if policy == "ignored" || policy == "protected" {
				if err := st.SetContainerFlag(ctx, "a:db", policy, true); err != nil {
					t.Fatal(err)
				}
			}
			s := New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
			s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				t.Error("blocked stack was dispatched")
				return nil, context.Canceled
			})}
			r := httptest.NewRecorder()
			s.Handler().ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/containers/a:web/update", bytes.NewBufferString(`{"confirm":true,"scope":"stack"}`)))
			if r.Code != 409 {
				t.Fatalf("status %d: %s", r.Code, r.Body.String())
			}
			r = httptest.NewRecorder()
			s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/containers/a:web/preflight?scope=stack", nil))
			var pf domain.UpdatePreflight
			if err := json.Unmarshal(r.Body.Bytes(), &pf); err != nil || pf.CanUpdate {
				t.Fatalf("preflight allowed blocked stack: %+v %v", pf, err)
			}
		})
	}
}

func TestTerminalStatusPublishesAfterPersistence(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "events.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := []byte("abcdefghijklmnopqrstuvwxyz123456")
	if err := st.RegisterAgent(t.Context(), domain.Agent{ID: "a", Name: "a"}, secret); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateJob(t.Context(), domain.Job{ID: "job", AgentID: "a", TargetKey: "target", Status: domain.StatusRunning, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s := New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ch := make(chan domain.Event, 16)
	s.subscribe("job", ch)
	defer s.unsubscribe("job", ch)
	body := []byte(`{"agent_id":"a","control_job_id":"job","status":"succeeded"}`)
	req := httptest.NewRequest("POST", "/api/v1/agent-events", bytes.NewReader(body))
	auth.Sign(req, "a", secret, body, time.Now())
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != 202 {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	select {
	case ev := <-ch:
		job, err := st.Job(t.Context(), "job")
		if err != nil || job.Status != domain.StatusSucceeded || ev.Message != "Job status: succeeded" {
			t.Fatalf("bad event or stale status: %+v %+v %v", ev, job, err)
		}
	default:
		t.Fatal("terminal status was not published")
	}
	events, err := st.Events(t.Context(), "job", 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("terminal status not available for reconnect: %+v %v", events, err)
	}
}

func TestRefreshAllowsTheAgentInventoryDeadline(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "timeout.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := domain.Agent{ID: "a", Name: "a", BaseURL: "http://agent"}
	if err := st.RegisterAgent(t.Context(), a, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	s := New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.client = &http.Client{Timeout: 20 * time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 90*time.Second {
			t.Errorf("refresh still uses short timeout: %v", deadline)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`{}`)), Header: make(http.Header)}, nil
	})}
	if err := s.agentCall(t.Context(), a, "POST", "/refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	if s.client.Timeout != 20*time.Second {
		t.Fatal("refresh changed timeout for all requests")
	}
}

func TestRegisterAcceptsDocumentedSnakeCasePayload(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	body := []byte(`{"id":"lxc-101","name":"apps","base_url":"http://agent:9090","secret":"abcdefghijklmnopqrstuvwxyz123456","version":"0.1.0","read_only":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer bootstrap")
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	agents, err := st.Agents(req.Context())
	if err != nil || len(agents) != 1 || agents[0].BaseURL != "http://agent:9090" || !agents[0].ReadOnly {
		t.Fatalf("unexpected agent: %+v %v", agents, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDryRunDispatchesSignedDomainRequest(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := store.Open(filepath.Join(t.TempDir(), "dispatch.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	secret := []byte("abcdefghijklmnopqrstuvwxyz123456")
	if err = st.RegisterAgent(ctx, domain.Agent{ID: "a1", Name: "host", BaseURL: "http://agent:9090"}, secret); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = st.Heartbeat(ctx, domain.Heartbeat{AgentID: "a1", SentAt: now}); err != nil {
		t.Fatal(err)
	}
	inv := domain.Inventory{AgentID: "a1", SyncedAt: now, Containers: []domain.Container{{ID: "a1:c1", DockerID: "c1", Name: "web", Image: "nginx:1", RuntimeStatus: "running", ManagementKind: "docker-run", Manageable: true}}}
	if err = st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	server := New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, verifyErr := auth.Verify(req, secret, time.Now())
		if verifyErr != nil {
			t.Fatalf("unsigned agent request: %v", verifyErr)
		}
		if req.URL.Path != "/dry-run" || !bytes.Contains(body, []byte(`"container_id":"c1"`)) {
			t.Fatalf("unexpected dispatch %s %s", req.URL.Path, body)
		}
		payload := `{"job_id":"agent-job-1","plan":"safe plan"}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(payload)), Header: make(http.Header)}, nil
	})}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/containers/a1:c1/dry-run", bytes.NewBufferString(`{"scope":"container"}`))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	jobs, err := st.Jobs(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != domain.StatusSucceeded || jobs[0].DryRunPlan != "safe plan" {
		t.Fatalf("unexpected jobs: %+v %v", jobs, err)
	}
}

func TestPreflightExplainsReadOnlyAgent(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := store.Open(filepath.Join(t.TempDir(), "preflight.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	if err = st.RegisterAgent(ctx, domain.Agent{ID: "a1", Name: "host", BaseURL: "http://agent:9090", ReadOnly: true}, []byte("abcdefghijklmnopqrstuvwxyz123456")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = st.Heartbeat(ctx, domain.Heartbeat{AgentID: "a1", ReadOnly: true, SentAt: now}); err != nil {
		t.Fatal(err)
	}
	inv := domain.Inventory{AgentID: "a1", SyncedAt: now, Containers: []domain.Container{{ID: "a1:c1", DockerID: "c1", Name: "web", Image: "nginx:1", CurrentDigest: "sha256:old", RemoteDigest: "sha256:new", UpdateAvailable: true, RuntimeStatus: "running", ManagementKind: "docker-run", Manageable: true, Labels: map[string]string{"io.dockpulse.manage": "true"}}}}
	if err = st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/a1:c1/preflight?scope=container", nil)
	res := httptest.NewRecorder()
	New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	var pf domain.UpdatePreflight
	if err := json.Unmarshal(res.Body.Bytes(), &pf); err != nil {
		t.Fatal(err)
	}
	if !pf.CanDryRun || pf.CanUpdate {
		t.Fatalf("expected dry-run allowed and update blocked: %+v", pf)
	}
	found := false
	for _, check := range pf.Checks {
		if check.Key == "agent_mode" && check.Status == "block" {
			found = true
		}
	}
	if !found {
		t.Fatalf("read-only check missing from preflight: %+v", pf.Checks)
	}
}

func TestHeartbeatUsesControlPlaneTime(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := store.Open(filepath.Join(t.TempDir(), "heartbeat.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := []byte("abcdefghijklmnopqrstuvwxyz123456")
	if err = st.RegisterAgent(t.Context(), domain.Agent{ID: "a1", Name: "host", BaseURL: "http://agent"}, secret); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"agent_id":"a1","version":"0.1.0","sent_at":"2099-01-01T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/heartbeat", bytes.NewReader(body))
	auth.Sign(req, "a1", secret, body, time.Now())
	res := httptest.NewRecorder()
	New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	agent, err := st.Agent(t.Context(), "a1")
	if err != nil || agent.LastHeartbeat.After(time.Now().Add(time.Second)) {
		t.Fatalf("untrusted heartbeat timestamp was persisted: %+v %v", agent, err)
	}
}
