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

	"github.com/dockpulse/dockpulse/internal/domain"
	"github.com/dockpulse/dockpulse/internal/store"
)

func reviewServer(t *testing.T, members []domain.Container) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "review.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.RegisterAgent(t.Context(), domain.Agent{ID: "a", Name: "host", BaseURL: "http://agent"}, []byte("abcdefghijklmnopqrstuvwxyz123456")); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertInventory(t.Context(), domain.Inventory{AgentID: "a", SyncedAt: time.Now(), Containers: members}); err != nil {
		t.Fatal(err)
	}
	return New(st, "bootstrap", "", slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func TestServiceUpdateBlocksProtectedReplica(t *testing.T) {
	members := []domain.Container{
		{DockerID: "one", Name: "app-web-1", Image: "nginx", RuntimeStatus: "running", ManagementKind: "compose", ComposeProject: "app", ComposeService: "web", ComposeWorkingDir: "/app", Manageable: true},
		{DockerID: "two", Name: "app-web-2", Image: "nginx", RuntimeStatus: "running", ManagementKind: "compose", ComposeProject: "app", ComposeService: "web", ComposeWorkingDir: "/app", Manageable: true},
	}
	s, st := reviewServer(t, members)
	if err := st.SetContainerFlag(t.Context(), "a:two", "protected", true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 202, Body: io.NopCloser(bytes.NewBufferString(`{"job_id":"job"}`)), Header: make(http.Header)}, nil
	})}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest("POST", "/api/v1/containers/a:one/update", bytes.NewBufferString(`{"confirm":true,"scope":"service"}`)))
	if res.Code != 409 || calls != 0 {
		t.Fatalf("regression: %d %s calls=%d", res.Code, res.Body.String(), calls)
	}
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest("GET", "/api/v1/containers/a:one/preflight?scope=service", nil))
	var pf domain.UpdatePreflight
	if err := json.Unmarshal(res.Body.Bytes(), &pf); err != nil || pf.CanUpdate {
		t.Fatalf("preflight ignored protected replica: %s", res.Body.String())
	}
}

func TestDispatchOnlyReleasesLockOnExplicitRejection(t *testing.T) {
	for _, status := range []int{409, 500, 200, 202} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, st := reviewServer(t, []domain.Container{{DockerID: "one", Name: "app", RuntimeStatus: "running", ManagementKind: "docker-run", Manageable: true}})
			s.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				body := "invalid response"
				if status == 202 {
					body = "{}"
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(body)), Header: make(http.Header)}, nil
			})}
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, httptest.NewRequest("POST", "/api/v1/containers/a:one/update", bytes.NewBufferString(`{"confirm":true}`)))
			jobs, err := st.Jobs(t.Context(), 10)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("missing job: %+v %v", jobs, err)
			}
			want := domain.StatusQueued
			if status == 409 {
				want = domain.StatusFailed
			}
			if jobs[0].Status != want {
				t.Fatalf("status %d incorrectly set job to %s", status, jobs[0].Status)
			}
		})
	}
}

func TestLostAcknowledgementRetainsLock(t *testing.T) {
	s, st := reviewServer(t, []domain.Container{{DockerID: "one", Name: "app", Image: "nginx", RuntimeStatus: "running", ManagementKind: "docker-run", Manageable: true}})
	s.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest("POST", "/api/v1/containers/a:one/update", bytes.NewBufferString(`{"confirm":true}`)))
	jobs, err := st.Jobs(t.Context(), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != domain.StatusQueued {
		t.Fatalf("regression: %+v %v", jobs, err)
	}
	retry := jobs[0]
	retry.ID = "retry"
	retry.Status = domain.StatusQueued
	if err = st.CreateJob(t.Context(), retry); err == nil {
		t.Fatal("uncertain dispatch released its lock")
	}
}
