package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/dockpulse/dockpulse/internal/auth"
	"github.com/dockpulse/dockpulse/internal/domain"
	"github.com/dockpulse/dockpulse/internal/store"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerSensitivePolicy(t *testing.T) {
	for _, scope := range []string{"container", "service", "stack"} {
		t.Run(scope, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "policy.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			secret := []byte("abcdefghijklmnopqrstuvwxyz123456")
			if err := st.RegisterAgent(t.Context(), domain.Agent{ID: "a", Name: "host", BaseURL: "http://agent"}, secret); err != nil {
				t.Fatal(err)
			}
			if err := st.Heartbeat(t.Context(), domain.Heartbeat{AgentID: "a", SentAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			c := domain.Container{DockerID: "db", Name: "db", Image: "postgres:17", RuntimeStatus: "running", ManagementKind: "docker-run", Manageable: true, Sensitive: true}
			if scope != "container" {
				c.ManagementKind = "compose"
				c.ComposeProject = "app"
				c.ComposeService = "database"
				c.ComposeWorkingDir = "/app"
			}
			if err := st.UpsertInventory(t.Context(), domain.Inventory{AgentID: "a", SyncedAt: time.Now(), Containers: []domain.Container{c}}); err != nil {
				t.Fatal(err)
			}
			s := New(st, "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
			calls := 0
			s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body, err := auth.Verify(r, secret, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				var req domain.ActionRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatal(err)
				}
				key := "db"
				if scope != "container" {
					key = "database"
				}
				if len(req.SensitivePolicy) != 1 || !req.SensitivePolicy[key] {
					t.Fatalf("unexpected signed policy: %+v", req)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`{"job_id":"agent-job","plan":"safe plan"}`))}, nil
			})}
			request := func(method, path, body string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
				return w
			}
			action := `{"scope":"` + scope + `","confirm":true}`
			if w := request("POST", "/api/v1/containers/a:db/update", action); w.Code != 409 {
				t.Fatalf("default not blocked: %d %s", w.Code, w.Body.String())
			}
			if w := request("POST", "/api/v1/containers/a:db/allow-sensitive", "{}"); w.Code != 200 {
				t.Fatalf("allow failed: %s", w.Body.String())
			}
			if w := request("GET", "/api/v1/containers/a:db/preflight?scope="+scope, ""); w.Code != 200 {
				t.Fatal(w.Body.String())
			} else {
				var pf domain.UpdatePreflight
				if err := json.Unmarshal(w.Body.Bytes(), &pf); err != nil || !pf.CanUpdate {
					t.Fatalf("preflight still blocked: %s", w.Body.String())
				}
			}
			if w := request("POST", "/api/v1/containers/a:db/update", action); w.Code != 202 {
				t.Fatalf("allowed update failed: %d %s", w.Code, w.Body.String())
			}
			if calls != 1 {
				t.Fatalf("expected one dispatch: %d", calls)
			}
			for _, hb := range []domain.Heartbeat{
				{AgentID: "a", SentAt: time.Now(), ReadOnly: true},
				{AgentID: "a", SentAt: time.Now().Add(-2 * time.Minute)},
			} {
				if err := st.Heartbeat(t.Context(), hb); err != nil {
					t.Fatal(err)
				}
				if w := request("POST", "/api/v1/containers/a:db/update", action); w.Code != 409 {
					t.Fatal("sensitive permission bypassed agent state")
				}
			}
			if err := st.Heartbeat(t.Context(), domain.Heartbeat{AgentID: "a", SentAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			// Other management safeguards remain independent of sensitive permission.
			for _, flag := range []string{"ignored", "protected"} {
				if err := st.SetContainerFlag(t.Context(), "a:db", flag, true); err != nil {
					t.Fatal(err)
				}
				if w := request("POST", "/api/v1/containers/a:db/update", action); w.Code != 409 {
					t.Fatalf("bypassed %s", flag)
				}
				if err := st.SetContainerFlag(t.Context(), "a:db", flag, false); err != nil {
					t.Fatal(err)
				}
			}
			if w := request("POST", "/api/v1/containers/a:db/block-sensitive", "{}"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			for _, kind := range []string{"update", "dry-run"} {
				if w := request("POST", "/api/v1/containers/a:db/"+kind, action); w.Code != 409 {
					t.Fatalf("revoked policy dispatched %s", kind)
				}
			}
			if calls != 1 {
				t.Fatal("blocked action reached agent")
			}
			if w := request("POST", "/api/v1/containers/missing/allow-sensitive", "{}"); w.Code != 404 {
				t.Fatal("missing target accepted")
			}
		})
	}
}
