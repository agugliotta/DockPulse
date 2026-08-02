package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dockpulse/dockpulse/internal/auth"
	"github.com/dockpulse/dockpulse/internal/domain"
	"github.com/dockpulse/dockpulse/internal/store"
)

type Config struct {
	ID, Name, BaseURL, Secret, ControlURL, BootstrapToken, Version string
	ReadOnly                                                       bool
	HeartbeatInterval, InventoryInterval                           time.Duration
}
type localJob struct {
	Job    domain.Job
	Events []domain.Event
}
type Agent struct {
	cfg       Config
	discovery Discovery
	updater   Updater
	client    *http.Client
	log       *slog.Logger
	mu        sync.RWMutex
	jobs      map[string]*localJob
	inventory domain.Inventory
	replay    *auth.ReplayGuard
}

const maxLocalJobs = 200

func New(cfg Config, d Discovery, u Updater, log *slog.Logger) *Agent {
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.InventoryInterval == 0 {
		cfg.InventoryInterval = 5 * time.Minute
	}
	return &Agent{cfg: cfg, discovery: d, updater: u, client: &http.Client{Timeout: 20 * time.Second}, log: log, jobs: make(map[string]*localJob), replay: auth.NewReplayGuard()}
}

func (a *Agent) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", a.health)
	m.HandleFunc("GET /inventory", a.authed(a.getInventory))
	m.HandleFunc("POST /heartbeat", a.authed(a.localHeartbeat))
	m.HandleFunc("POST /refresh", a.authed(a.refresh))
	m.HandleFunc("POST /dry-run", a.authed(a.dryRun))
	m.HandleFunc("POST /update", a.authed(a.update))
	m.HandleFunc("GET /jobs/{id}", a.authed(a.getJob))
	m.HandleFunc("GET /jobs/{id}/logs", a.authed(a.getLogs))
	return m
}

type handler func(http.ResponseWriter, *http.Request, []byte)

func (a *Agent) authed(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(auth.HeaderAgent) != a.cfg.ID {
			agentProblem(w, 401, "agent identity mismatch")
			return
		}
		body, err := a.replay.Verify(r, []byte(a.cfg.Secret), time.Now())
		if err != nil {
			agentProblem(w, 401, err.Error())
			return
		}
		next(w, r, body)
	}
}
func agentJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func agentProblem(w http.ResponseWriter, status int, msg string) {
	agentJSON(w, status, map[string]any{"error": msg, "status": status})
}
func (a *Agent) health(w http.ResponseWriter, r *http.Request) {
	agentJSON(w, 200, map[string]any{"status": "ok", "agent_id": a.cfg.ID, "read_only": a.cfg.ReadOnly})
}
func (a *Agent) getInventory(w http.ResponseWriter, r *http.Request, _ []byte) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	agentJSON(w, 200, a.inventory)
}
func (a *Agent) localHeartbeat(w http.ResponseWriter, r *http.Request, _ []byte) {
	agentJSON(w, 200, map[string]string{"status": "ok"})
}
func (a *Agent) refresh(w http.ResponseWriter, r *http.Request, body []byte) {
	var in struct {
		CheckUpdates bool `json:"check_updates"`
	}
	_ = json.Unmarshal(body, &in)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	inv, err := a.discovery.Inventory(ctx, a.cfg.ID, in.CheckUpdates)
	if err != nil {
		agentProblem(w, 500, err.Error())
		return
	}
	a.mu.Lock()
	a.inventory = inv
	a.mu.Unlock()
	agentJSON(w, 200, inv)
}
func (a *Agent) dryRun(w http.ResponseWriter, r *http.Request, body []byte) {
	var req domain.ActionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		agentProblem(w, 400, err.Error())
		return
	}
	plan, err := a.updater.DryRun(r.Context(), req)
	if err != nil {
		agentProblem(w, 409, err.Error())
		return
	}
	b, _ := json.MarshalIndent(plan, "", "  ")
	id := store.ID("agentjob")
	j := domain.Job{ID: id, Action: "dry-run", Status: domain.StatusSucceeded, DryRunPlan: string(b), CreatedAt: time.Now()}
	a.mu.Lock()
	a.jobs[id] = &localJob{Job: j}
	a.pruneJobsLocked()
	a.mu.Unlock()
	agentJSON(w, 200, domain.ActionResponse{JobID: id, Plan: string(b)})
}
func (a *Agent) update(w http.ResponseWriter, r *http.Request, body []byte) {
	if a.cfg.ReadOnly {
		agentProblem(w, 409, "agent is read-only")
		return
	}
	var req domain.ActionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		agentProblem(w, 400, err.Error())
		return
	}
	if !req.Confirm {
		agentProblem(w, 400, "explicit confirmation required")
		return
	}
	if req.ControlJobID == "" || req.ContainerID == "" {
		agentProblem(w, 400, "control_job_id and container_id are required")
		return
	}
	if _, err := a.updater.DryRun(r.Context(), req); err != nil {
		agentProblem(w, 409, err.Error())
		return
	}
	id := store.ID("agentjob")
	j := domain.Job{ID: id, Action: "update", Status: domain.StatusQueued, CreatedAt: time.Now()}
	a.mu.Lock()
	a.jobs[id] = &localJob{Job: j}
	a.pruneJobsLocked()
	a.mu.Unlock()
	go a.execute(id, req)
	agentJSON(w, 202, domain.ActionResponse{JobID: id})
}
func (a *Agent) execute(id string, req domain.ActionRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	now := time.Now()
	a.mu.Lock()
	a.jobs[id].Job.Status = domain.StatusRunning
	a.jobs[id].Job.StartedAt = &now
	a.mu.Unlock()
	a.emit(req.ControlJobID, id, "info", "update started", domain.StatusRunning, "", "")
	err := a.updater.Execute(ctx, req, func(level, msg string) { a.emit(req.ControlJobID, id, level, msg, "", "", "") })
	status := domain.StatusSucceeded
	errText := ""
	if err != nil {
		status = domain.StatusFailed
		errText = err.Error()
		a.emit(req.ControlJobID, id, "error", errText, "", "", "")
	} else {
		a.emit(req.ControlJobID, id, "info", "update completed successfully", "", "", "")
	}
	finished := time.Now()
	a.mu.Lock()
	a.jobs[id].Job.Status = status
	a.jobs[id].Job.Error = errText
	a.jobs[id].Job.FinishedAt = &finished
	a.mu.Unlock()
	a.emit(req.ControlJobID, id, "", "", status, "", errText)
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer refreshCancel()
	if inv, e := a.discovery.Inventory(refreshCtx, a.cfg.ID, true); e == nil {
		a.mu.Lock()
		a.inventory = inv
		a.mu.Unlock()
		_ = a.sendHeartbeat(refreshCtx, &inv)
	}
}
func (a *Agent) emit(controlID, localID, level, msg, status, plan, errText string) {
	if msg != "" {
		a.mu.Lock()
		j := a.jobs[localID]
		j.Events = append(j.Events, domain.Event{ID: int64(len(j.Events) + 1), JobID: localID, Level: level, Message: msg, CreatedAt: time.Now()})
		a.mu.Unlock()
		a.log.Info("job_event", "job_id", localID, "control_job_id", controlID, "level", level, "message", msg)
	}
	ev := domain.AgentEvent{AgentID: a.cfg.ID, ControlJobID: controlID, Status: status, Level: level, Message: msg, Plan: plan, Error: errText}
	attempts := 1
	if status == domain.StatusSucceeded || status == domain.StatusFailed {
		attempts = 4
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := a.controlCall(ctx, "POST", "/api/v1/agent-events", ev, nil)
		cancel()
		if err == nil {
			return
		}
		a.log.Error("event_delivery_failed", "job_id", localID, "attempt", attempt, "error", err)
		if attempt < attempts {
			time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
		}
	}
}
func (a *Agent) getJob(w http.ResponseWriter, r *http.Request, _ []byte) {
	a.mu.RLock()
	j, ok := a.jobs[r.PathValue("id")]
	var job domain.Job
	if ok {
		job = j.Job
	}
	a.mu.RUnlock()
	if !ok {
		agentProblem(w, 404, "job not found")
		return
	}
	agentJSON(w, 200, job)
}

func (a *Agent) pruneJobsLocked() {
	for len(a.jobs) > maxLocalJobs {
		var oldestID string
		var oldest time.Time
		for id, job := range a.jobs {
			if job.Job.Status != domain.StatusSucceeded && job.Job.Status != domain.StatusFailed {
				continue
			}
			if oldestID == "" || job.Job.CreatedAt.Before(oldest) {
				oldestID, oldest = id, job.Job.CreatedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(a.jobs, oldestID)
	}
}
func (a *Agent) getLogs(w http.ResponseWriter, r *http.Request, _ []byte) {
	a.mu.RLock()
	j, ok := a.jobs[r.PathValue("id")]
	if !ok {
		a.mu.RUnlock()
		agentProblem(w, 404, "job not found")
		return
	}
	events := append([]domain.Event{}, j.Events...)
	a.mu.RUnlock()
	agentJSON(w, 200, events)
}

func (a *Agent) RunControlLoop(ctx context.Context) {
	registered := a.sendHeartbeat(ctx, nil) == nil
	heartbeat := time.NewTicker(a.cfg.HeartbeatInterval)
	defer heartbeat.Stop()
	inventory := time.NewTicker(a.cfg.InventoryInterval)
	defer inventory.Stop()
	refresh := func() {
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		inv, err := a.discovery.Inventory(c, a.cfg.ID, true)
		if err != nil {
			a.log.Error("inventory_failed", "error", err)
			return
		}
		a.mu.Lock()
		a.inventory = inv
		a.mu.Unlock()
		if err = a.sendHeartbeat(c, &inv); err != nil {
			a.log.Error("inventory_delivery_failed", "error", err)
		}
	}
	if registered {
		refresh()
	}
	for {
		if !registered {
			if err := a.register(ctx); err != nil {
				a.log.Error("registration_failed", "error", err)
			} else {
				registered = true
				a.log.Info("agent_registered", "agent_id", a.cfg.ID)
				refresh()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := a.sendHeartbeat(ctx, nil); err != nil {
				a.log.Error("heartbeat_failed", "error", err)
				registered = false
			}
		case <-inventory.C:
			refresh()
		}
	}
}
func (a *Agent) register(ctx context.Context) error {
	payload := struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		BaseURL  string `json:"base_url"`
		Secret   string `json:"secret"`
		Version  string `json:"version"`
		ReadOnly bool   `json:"read_only"`
	}{a.cfg.ID, a.cfg.Name, a.cfg.BaseURL, a.cfg.Secret, a.cfg.Version, a.cfg.ReadOnly}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(a.cfg.ControlURL, "/")+"/api/v1/agents/register", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.BootstrapToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		v, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("control returned %d: %s", res.StatusCode, v)
	}
	return nil
}
func (a *Agent) sendHeartbeat(ctx context.Context, inv *domain.Inventory) error {
	return a.controlCall(ctx, "POST", "/api/v1/agents/heartbeat", domain.Heartbeat{AgentID: a.cfg.ID, Version: a.cfg.Version, ReadOnly: a.cfg.ReadOnly, Inventory: inv, SentAt: time.Now().UTC()}, nil)
}
func (a *Agent) controlCall(ctx context.Context, method, path string, input, output any) error {
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.cfg.ControlURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.Sign(req, a.cfg.ID, []byte(a.cfg.Secret), b, time.Now())
	res, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, auth.MaxBodyBytes))
	if res.StatusCode >= 300 {
		return fmt.Errorf("control returned %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	if output != nil {
		return json.Unmarshal(data, output)
	}
	return nil
}
