package control

import (
	"bytes"
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
