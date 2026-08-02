package control

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dockpulse/dockpulse/internal/auth"
	"github.com/dockpulse/dockpulse/internal/domain"
	"github.com/dockpulse/dockpulse/internal/store"
)

type Server struct {
	store     *store.Store
	bootstrap string
	client    *http.Client
	log       *slog.Logger
	webDir    string
	subsMu    sync.Mutex
	subs      map[string]map[chan domain.Event]struct{}
	replay    *auth.ReplayGuard
}

func New(st *store.Store, bootstrap, webDir string, log *slog.Logger) *Server {
	return &Server{store: st, bootstrap: bootstrap, client: &http.Client{Timeout: 20 * time.Second}, log: log, webDir: webDir, subs: make(map[string]map[chan domain.Event]struct{}), replay: auth.NewReplayGuard()}
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/health", s.health)
	m.HandleFunc("POST /api/v1/agents/register", s.register)
	m.HandleFunc("POST /api/v1/agents/heartbeat", s.agentAuthenticated(s.heartbeat))
	m.HandleFunc("POST /api/v1/agent-events", s.agentAuthenticated(s.agentEvent))
	m.HandleFunc("GET /api/v1/agents", s.listAgents)
	m.HandleFunc("GET /api/v1/agents/{id}", s.getAgent)
	m.HandleFunc("DELETE /api/v1/agents/{id}", s.deleteAgent)
	m.HandleFunc("GET /api/v1/agents/{id}/containers", s.agentContainers)
	m.HandleFunc("POST /api/v1/agents/{id}/refresh", s.refresh)
	m.HandleFunc("GET /api/v1/containers", s.listContainers)
	m.HandleFunc("GET /api/v1/containers/{id}", s.getContainer)
	m.HandleFunc("POST /api/v1/containers/{id}/dry-run", s.action("dry-run"))
	m.HandleFunc("POST /api/v1/containers/{id}/update", s.action("update"))
	m.HandleFunc("POST /api/v1/containers/{id}/ignore", s.flag("ignored", true))
	m.HandleFunc("POST /api/v1/containers/{id}/unignore", s.flag("ignored", false))
	m.HandleFunc("POST /api/v1/containers/{id}/protect", s.flag("protected", true))
	m.HandleFunc("POST /api/v1/containers/{id}/unprotect", s.flag("protected", false))
	m.HandleFunc("POST /api/v1/stacks/{agent}/{project}/ignore", s.stackFlag("ignored", true))
	m.HandleFunc("POST /api/v1/stacks/{agent}/{project}/unignore", s.stackFlag("ignored", false))
	m.HandleFunc("POST /api/v1/stacks/{agent}/{project}/protect", s.stackFlag("protected", true))
	m.HandleFunc("POST /api/v1/stacks/{agent}/{project}/unprotect", s.stackFlag("protected", false))
	m.HandleFunc("GET /api/v1/jobs", s.listJobs)
	m.HandleFunc("GET /api/v1/jobs/{id}", s.getJob)
	m.HandleFunc("GET /api/v1/jobs/{id}/logs", s.jobLogs)
	m.HandleFunc("GET /api/v1/jobs/{id}/events", s.jobEvents)
	if s.webDir != "" {
		m.HandleFunc("GET /", s.static)
	}
	return s.logging(m)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(auth.HeaderRequest)
		if id == "" {
			id = store.ID("req")
		}
		r.Header.Set(auth.HeaderRequest, id)
		w.Header().Set(auth.HeaderRequest, id)
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Info("http_request", "request_id", id, "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "status": status})
}
func decode(body io.Reader, v any) error {
	d := json.NewDecoder(io.LimitReader(body, auth.MaxBodyBytes))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok", "service": "dockpulse-control"})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(s.bootstrap)) != 1 {
		problem(w, 401, "invalid bootstrap token")
		return
	}
	var in struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		BaseURL  string `json:"base_url"`
		Secret   string `json:"secret"`
		Version  string `json:"version"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := decode(r.Body, &in); err != nil {
		problem(w, 400, err.Error())
		return
	}
	if in.ID == "" || in.Name == "" || in.Secret == "" || len(in.Secret) < 32 {
		problem(w, 400, "id, name and a secret of at least 32 characters are required")
		return
	}
	if !strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://") {
		problem(w, 400, "base_url must be http(s)")
		return
	}
	a := domain.Agent{ID: in.ID, Name: in.Name, BaseURL: in.BaseURL, Version: in.Version, ReadOnly: in.ReadOnly}
	if err := s.store.RegisterAgent(r.Context(), a, []byte(in.Secret)); err != nil {
		problem(w, 409, err.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"id": in.ID, "status": "registered"})
}

type authHandler func(http.ResponseWriter, *http.Request, []byte)

func (s *Server) agentAuthenticated(next authHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(auth.HeaderAgent)
		secret, err := s.store.AgentSecret(r.Context(), id)
		if err != nil {
			problem(w, 401, "unknown agent")
			return
		}
		body, err := s.replay.Verify(r, secret, time.Now())
		if err != nil {
			problem(w, 401, err.Error())
			return
		}
		next(w, r, body)
	}
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request, body []byte) {
	var h domain.Heartbeat
	if err := json.Unmarshal(body, &h); err != nil {
		problem(w, 400, err.Error())
		return
	}
	if h.AgentID != r.Header.Get(auth.HeaderAgent) {
		problem(w, 403, "agent identity mismatch")
		return
	}
	// Freshness is a control-plane observation. Agent clocks can drift or be
	// malicious; never let a client-provided future timestamp keep it healthy.
	receivedAt := time.Now()
	h.SentAt = receivedAt
	if err := s.store.Heartbeat(r.Context(), h); err != nil {
		problem(w, 500, err.Error())
		return
	}
	if h.Inventory != nil {
		h.Inventory.AgentID = h.AgentID
		h.Inventory.SyncedAt = receivedAt
		if err := s.store.UpsertInventory(r.Context(), *h.Inventory); err != nil {
			problem(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]string{"status": "accepted"})
}

func (s *Server) agentEvent(w http.ResponseWriter, r *http.Request, body []byte) {
	var ev domain.AgentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		problem(w, 400, err.Error())
		return
	}
	if ev.AgentID != r.Header.Get(auth.HeaderAgent) {
		problem(w, 403, "agent identity mismatch")
		return
	}
	job, err := s.store.Job(r.Context(), ev.ControlJobID)
	if err != nil || job.AgentID != ev.AgentID {
		problem(w, 403, "job does not belong to this agent")
		return
	}
	if ev.Message != "" {
		e := domain.Event{JobID: ev.ControlJobID, Level: ev.Level, Message: ev.Message, CreatedAt: time.Now()}
		id, err := s.store.AddEvent(r.Context(), e)
		if err != nil {
			problem(w, 500, err.Error())
			return
		}
		e.ID = id
		s.publish(e)
	}
	if ev.Status != "" {
		if err := s.store.UpdateJob(r.Context(), ev.ControlJobID, ev.Status, ev.Plan, ev.Error); err != nil {
			problem(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 202, map[string]string{"status": "accepted"})
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Agents(r.Context())
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	if v == nil {
		v = []domain.Agent{}
	}
	writeJSON(w, 200, v)
}
func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Agent(r.Context(), r.PathValue("id"))
	if e != nil {
		problem(w, 404, "agent not found")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "true" {
		problem(w, 400, "confirm=true is required")
		return
	}
	if e := s.store.DeleteAgent(r.Context(), r.PathValue("id")); e != nil {
		problem(w, 409, e.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Containers(r.Context(), r.URL.Query().Get("agent_id"))
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	if v == nil {
		v = []domain.Container{}
	}
	writeJSON(w, 200, v)
}
func (s *Server) agentContainers(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Containers(r.Context(), r.PathValue("id"))
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	if v == nil {
		v = []domain.Container{}
	}
	writeJSON(w, 200, v)
}
func (s *Server) getContainer(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Container(r.Context(), r.PathValue("id"))
	if e != nil {
		problem(w, 404, "container not found")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) flag(flag string, value bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.SetContainerFlag(r.Context(), r.PathValue("id"), flag, value); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				problem(w, 404, "container not found")
				return
			}
			problem(w, 500, err.Error())
			return
		}
		c, _ := s.store.Container(r.Context(), r.PathValue("id"))
		writeJSON(w, 200, c)
	}
}

func (s *Server) stackFlag(flag string, value bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.SetStackFlag(r.Context(), r.PathValue("agent"), r.PathValue("project"), flag, value); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				problem(w, 404, "stack not found")
				return
			}
			problem(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"agent_id": r.PathValue("agent"), "project": r.PathValue("project"), flag: value})
	}
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.Agent(r.Context(), r.PathValue("id"))
	if err != nil {
		problem(w, 404, "agent not found")
		return
	}
	var inv domain.Inventory
	if err = s.agentCall(r.Context(), a, "POST", "/refresh", map[string]bool{"check_updates": true}, &inv); err != nil {
		problem(w, 502, err.Error())
		return
	}
	inv.AgentID = a.ID
	if inv.SyncedAt.IsZero() {
		inv.SyncedAt = time.Now()
	}
	if err = s.store.UpsertInventory(r.Context(), inv); err != nil {
		problem(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, inv)
}

func (s *Server) action(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := s.store.Container(r.Context(), r.PathValue("id"))
		if err != nil {
			problem(w, 404, "container not found")
			return
		}
		a, err := s.store.Agent(r.Context(), c.AgentID)
		if err != nil {
			problem(w, 404, "agent not found")
			return
		}
		var in struct {
			Confirm bool   `json:"confirm"`
			Scope   string `json:"scope"`
		}
		if err := decode(r.Body, &in); err != nil && !errors.Is(err, io.EOF) {
			problem(w, 400, err.Error())
			return
		}
		if in.Scope == "" {
			if c.ManagementKind == "compose" {
				in.Scope = "service"
			} else {
				in.Scope = "container"
			}
		}
		if in.Scope != "container" && in.Scope != "service" && in.Scope != "stack" {
			problem(w, 400, "scope must be container, service, or stack")
			return
		}
		if c.ManagementKind != "compose" && in.Scope != "container" {
			problem(w, 400, "docker-run targets only support container scope")
			return
		}
		if c.ManagementKind == "compose" && in.Scope == "container" {
			problem(w, 400, "compose targets support service or stack scope")
			return
		}
		if in.Scope == "stack" && c.ComposeProject == "" {
			problem(w, 409, "compose project is unavailable")
			return
		}
		if kind == "update" && !in.Confirm {
			problem(w, 400, "explicit confirmation is required")
			return
		}
		if kind == "update" && a.ReadOnly {
			problem(w, 409, "agent is read-only")
			return
		}
		if kind == "update" && (c.Ignored || c.Protected || (c.Sensitive && c.Labels["io.dockpulse.allow-sensitive"] != "true")) {
			problem(w, 409, "target is ignored, protected, or sensitive; change its policy before updating")
			return
		}
		if kind == "update" && !c.Manageable {
			problem(w, 409, "target is not safely manageable: "+c.SafetyReason)
			return
		}
		if a.Status == "offline" {
			problem(w, 409, "agent is offline")
			return
		}
		target := c.ID
		if in.Scope == "stack" && c.ComposeProject != "" {
			target = c.AgentID + ":stack:" + c.ComposeProject
		}
		j := domain.Job{ID: store.ID("job"), AgentID: c.AgentID, ContainerID: c.ID, TargetKey: target, Action: kind, Status: domain.StatusQueued, RequestedBy: user(r), CorrelationID: r.Header.Get(auth.HeaderRequest), CreatedAt: time.Now()}
		if err = s.store.CreateJob(r.Context(), j); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				problem(w, 409, "an update is already active for this target")
				return
			}
			problem(w, 500, err.Error())
			return
		}
		path := "/dry-run"
		if kind == "update" {
			path = "/update"
		}
		var out domain.ActionResponse
		err = s.agentCall(r.Context(), a, "POST", path, domain.ActionRequest{ControlJobID: j.ID, ContainerID: c.DockerID, Scope: in.Scope, Confirm: in.Confirm}, &out)
		if err != nil {
			_ = s.store.UpdateJob(r.Context(), j.ID, domain.StatusFailed, "", err.Error())
			problem(w, 502, err.Error())
			return
		}
		if err = s.store.SetAgentJob(r.Context(), j.ID, out.JobID); err != nil {
			problem(w, 500, "could not persist agent job: "+err.Error())
			return
		}
		if out.Plan != "" {
			_ = s.store.UpdateJob(r.Context(), j.ID, domain.StatusSucceeded, out.Plan, "")
		}
		j, _ = s.store.Job(r.Context(), j.ID)
		writeJSON(w, 202, j)
	}
}

func user(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("X-DockPulse-User"))
	if v == "" {
		return "local-admin"
	}
	return v
}
func (s *Server) agentCall(ctx context.Context, a domain.Agent, method, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, a.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	secret, err := s.store.AgentSecret(ctx, a.ID)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.HeaderRequest, store.ID("req"))
	auth.Sign(req, a.ID, secret, body, time.Now())
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent %s unreachable: %w", a.Name, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, auth.MaxBodyBytes))
	if res.StatusCode >= 300 {
		return fmt.Errorf("agent rejected request (%d): %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	if output != nil {
		return json.Unmarshal(b, output)
	}
	return nil
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v, e := s.store.Jobs(r.Context(), limit)
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	if v == nil {
		v = []domain.Job{}
	}
	writeJSON(w, 200, v)
}
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Job(r.Context(), r.PathValue("id"))
	if e != nil {
		problem(w, 404, "job not found")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) jobLogs(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Events(r.Context(), r.PathValue("id"), 0)
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	if v == nil {
		v = []domain.Event{}
	}
	writeJSON(w, 200, v)
}
func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	existing, _ := s.store.Events(r.Context(), r.PathValue("id"), after)
	for _, e := range existing {
		writeSSE(w, e)
		flusher.Flush()
	}
	ch := make(chan domain.Event, 16)
	s.subscribe(r.PathValue("id"), ch)
	defer s.unsubscribe(r.PathValue("id"), ch)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			writeSSE(w, e)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
func writeSSE(w io.Writer, e domain.Event) {
	b, _ := json.Marshal(e)
	fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", e.ID, b)
}
func (s *Server) subscribe(id string, ch chan domain.Event) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if s.subs[id] == nil {
		s.subs[id] = make(map[chan domain.Event]struct{})
	}
	s.subs[id][ch] = struct{}{}
}
func (s *Server) unsubscribe(id string, ch chan domain.Event) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	delete(s.subs[id], ch)
	close(ch)
}
func (s *Server) publish(e domain.Event) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs[e.JobID] {
		select {
		case ch <- e:
		default:
		}
	}
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		problem(w, 404, "not found")
		return
	}
	p := filepath.Join(s.webDir, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		http.ServeFile(w, r, p)
		return
	}
	index := filepath.Join(s.webDir, "index.html")
	if _, err := os.Stat(index); errors.Is(err, os.ErrNotExist) {
		problem(w, 404, "web application not built")
		return
	}
	http.ServeFile(w, r, index)
}
