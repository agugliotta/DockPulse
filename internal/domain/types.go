package domain

import "time"

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

type Agent struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	BaseURL       string    `json:"base_url"`
	Version       string    `json:"version"`
	ReadOnly      bool      `json:"read_only"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	LastSync      time.Time `json:"last_sync"`
	CreatedAt     time.Time `json:"created_at"`
}

type Container struct {
	ID                string            `json:"id"`
	AgentID           string            `json:"agent_id"`
	DockerID          string            `json:"docker_id"`
	Name              string            `json:"name"`
	Image             string            `json:"image"`
	Tag               string            `json:"tag"`
	CurrentDigest     string            `json:"current_digest,omitempty"`
	RemoteDigest      string            `json:"remote_digest,omitempty"`
	UpdateAvailable   bool              `json:"update_available"`
	DetectionMethod   string            `json:"detection_method,omitempty"`
	RuntimeStatus     string            `json:"runtime_status"`
	ManagementKind    string            `json:"management_kind"`
	ComposeProject    string            `json:"compose_project,omitempty"`
	ComposeService    string            `json:"compose_service,omitempty"`
	ComposeWorkingDir string            `json:"compose_working_dir,omitempty"`
	Labels            map[string]string `json:"labels"`
	Ignored           bool              `json:"ignored"`
	Protected         bool              `json:"protected"`
	Sensitive         bool              `json:"sensitive"`
	Manageable        bool              `json:"manageable"`
	SafetyReason      string            `json:"safety_reason,omitempty"`
	LastSync          time.Time         `json:"last_sync"`
}

type PreflightCheck struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Status  string `json:"status"` // pass, warn, block
	Message string `json:"message"`
}

type UpdatePreflight struct {
	ContainerID string           `json:"container_id"`
	AgentID     string           `json:"agent_id"`
	Scope       string           `json:"scope"`
	CanDryRun   bool             `json:"can_dry_run"`
	CanUpdate   bool             `json:"can_update"`
	Checks      []PreflightCheck `json:"checks"`
}

type Inventory struct {
	AgentID    string      `json:"agent_id"`
	Containers []Container `json:"containers"`
	SyncedAt   time.Time   `json:"synced_at"`
}

type Job struct {
	ID            string     `json:"id"`
	AgentJobID    string     `json:"agent_job_id,omitempty"`
	AgentID       string     `json:"agent_id"`
	ContainerID   string     `json:"container_id,omitempty"`
	TargetKey     string     `json:"target_key"`
	Action        string     `json:"action"`
	Status        string     `json:"status"`
	RequestedBy   string     `json:"requested_by"`
	CorrelationID string     `json:"correlation_id"`
	DryRunPlan    string     `json:"dry_run_plan,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type Event struct {
	ID        int64     `json:"id"`
	JobID     string    `json:"job_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type ActionRequest struct {
	ControlJobID string `json:"control_job_id"`
	ContainerID  string `json:"container_id"`
	Scope        string `json:"scope"` // container, service, stack
	Confirm      bool   `json:"confirm"`
}

type ActionResponse struct {
	JobID string `json:"job_id"`
	Plan  string `json:"plan,omitempty"`
}

type SelfUpdateRequest struct {
	ControlJobID  string `json:"control_job_id"`
	TargetVersion string `json:"target_version"`
	Confirm       bool   `json:"confirm"`
}

type Heartbeat struct {
	AgentID   string     `json:"agent_id"`
	Version   string     `json:"version"`
	ReadOnly  bool       `json:"read_only"`
	Inventory *Inventory `json:"inventory,omitempty"`
	SentAt    time.Time  `json:"sent_at"`
}

type AgentEvent struct {
	AgentID      string `json:"agent_id"`
	ControlJobID string `json:"control_job_id"`
	Status       string `json:"status,omitempty"`
	Level        string `json:"level,omitempty"`
	Message      string `json:"message,omitempty"`
	Plan         string `json:"plan,omitempty"`
	Error        string `json:"error,omitempty"`
}
