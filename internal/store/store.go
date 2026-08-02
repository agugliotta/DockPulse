package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/dockpulse/dockpulse/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

func Open(path, base64Key string) (*Store, error) {
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil || len(key) != 32 {
		return nil, errors.New("DOCKPULSE_ENCRYPTION_KEY must be base64-encoded 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, aead: aead}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		var version int
		if _, err := fmt.Sscanf(e.Name(), "%d_", &version); err != nil {
			return err
		}
		var exists int
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", version).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(b)); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", version, ts(time.Now()))
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func ID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parse(v sql.NullString) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v.String)
	return t
}

func (s *Store) encrypt(v []byte) ([]byte, error) {
	n := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, n); err != nil {
		return nil, err
	}
	return s.aead.Seal(n, n, v, nil), nil
}
func (s *Store) decrypt(v []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(v) < n {
		return nil, errors.New("invalid ciphertext")
	}
	return s.aead.Open(nil, v[:n], v[n:], nil)
}

func (s *Store) RegisterAgent(ctx context.Context, a domain.Agent, secret []byte) error {
	ciphertext, err := s.encrypt(secret)
	if err != nil {
		return err
	}
	now := ts(time.Now())
	_, err = s.db.ExecContext(ctx, `INSERT INTO agents(id,name,base_url,secret_cipher,version,read_only,last_heartbeat,created_at) VALUES(?,?,?,?,?,?,?,?)
	 ON CONFLICT(id) DO UPDATE SET name=excluded.name,base_url=excluded.base_url,secret_cipher=excluded.secret_cipher,version=excluded.version,read_only=excluded.read_only`, a.ID, a.Name, strings.TrimRight(a.BaseURL, "/"), ciphertext, a.Version, a.ReadOnly, now, now)
	return err
}

func (s *Store) AgentSecret(ctx context.Context, id string) ([]byte, error) {
	var b []byte
	if err := s.db.QueryRowContext(ctx, "SELECT secret_cipher FROM agents WHERE id=?", id).Scan(&b); err != nil {
		return nil, err
	}
	return s.decrypt(b)
}

func (s *Store) Heartbeat(ctx context.Context, h domain.Heartbeat) error {
	result, err := s.db.ExecContext(ctx, "UPDATE agents SET version=?,read_only=?,last_heartbeat=? WHERE id=?", h.Version, h.ReadOnly, ts(h.SentAt), h.AgentID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Agents(ctx context.Context) ([]domain.Agent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,base_url,version,read_only,last_heartbeat,last_sync,created_at FROM agents ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Agent
	now := time.Now()
	for rows.Next() {
		var a domain.Agent
		var hb, sync, created sql.NullString
		if err := rows.Scan(&a.ID, &a.Name, &a.BaseURL, &a.Version, &a.ReadOnly, &hb, &sync, &created); err != nil {
			return nil, err
		}
		a.LastHeartbeat = parse(hb)
		a.LastSync = parse(sync)
		a.CreatedAt = parse(created)
		age := now.Sub(a.LastHeartbeat)
		switch {
		case a.LastHeartbeat.IsZero() || age > 90*time.Second:
			a.Status = "offline"
		case age > 45*time.Second:
			a.Status = "degraded"
		default:
			a.Status = "healthy"
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Agent(ctx context.Context, id string) (domain.Agent, error) {
	as, err := s.Agents(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	for _, a := range as {
		if a.ID == id {
			return a, nil
		}
	}
	return domain.Agent{}, sql.ErrNoRows
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM agents WHERE id=?", id)
	return err
}

func (s *Store) UpsertInventory(ctx context.Context, inv domain.Inventory) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE containers SET runtime_status='removed',manageable=0,update_available=0 WHERE agent_id=?", inv.AgentID); err != nil {
		return err
	}
	for _, c := range inv.Containers {
		labels, _ := json.Marshal(c.Labels)
		id := c.ID
		if id == "" {
			id = inv.AgentID + ":" + c.DockerID
		}
		// Docker assigns a new ID whenever an update recreates a container. Keep
		// the control-plane identity stable by matching the daemon-unique name;
		// this preserves policy flags and audit references across recreation.
		var existingID string
		err = tx.QueryRowContext(ctx, "SELECT id FROM containers WHERE agent_id=? AND name=? LIMIT 1", inv.AgentID, c.Name).Scan(&existingID)
		if err == nil {
			id = existingID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO containers(id,agent_id,docker_id,name,image,tag,current_digest,remote_digest,update_available,detection_method,runtime_status,management_kind,compose_project,compose_service,compose_working_dir,labels_json,sensitive,manageable,safety_reason,last_sync)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET docker_id=excluded.docker_id,name=excluded.name,image=excluded.image,tag=excluded.tag,current_digest=excluded.current_digest,remote_digest=excluded.remote_digest,update_available=excluded.update_available,detection_method=excluded.detection_method,runtime_status=excluded.runtime_status,management_kind=excluded.management_kind,compose_project=excluded.compose_project,compose_service=excluded.compose_service,compose_working_dir=excluded.compose_working_dir,labels_json=excluded.labels_json,sensitive=excluded.sensitive,manageable=excluded.manageable,safety_reason=excluded.safety_reason,last_sync=excluded.last_sync`, id, inv.AgentID, c.DockerID, c.Name, c.Image, c.Tag, c.CurrentDigest, c.RemoteDigest, c.UpdateAvailable, c.DetectionMethod, c.RuntimeStatus, c.ManagementKind, c.ComposeProject, c.ComposeService, c.ComposeWorkingDir, string(labels), c.Sensitive, c.Manageable, c.SafetyReason, ts(inv.SyncedAt))
		if err != nil {
			return err
		}
		// A heartbeat should not create an identical snapshot forever. Retain a
		// new audit point only when the image evaluation actually changed.
		_, err = tx.ExecContext(ctx, `INSERT INTO image_snapshots(container_id,image,current_digest,remote_digest,update_available,observed_at)
			SELECT ?,?,?,?,?,? WHERE NOT EXISTS (
				SELECT 1 FROM image_snapshots WHERE id=(SELECT MAX(id) FROM image_snapshots WHERE container_id=?)
				AND image=? AND COALESCE(current_digest,'')=? AND COALESCE(remote_digest,'')=? AND update_available=?
			)`, id, c.Image, c.CurrentDigest, c.RemoteDigest, c.UpdateAvailable, ts(inv.SyncedAt), id, c.Image, c.CurrentDigest, c.RemoteDigest, c.UpdateAvailable)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM containers WHERE agent_id=? AND runtime_status='removed' AND NOT EXISTS (SELECT 1 FROM update_jobs WHERE update_jobs.container_id=containers.id)`, inv.AgentID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE agents SET last_sync=? WHERE id=?", ts(inv.SyncedAt), inv.AgentID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const containerSelect = `SELECT id,agent_id,docker_id,name,image,tag,current_digest,remote_digest,update_available,detection_method,runtime_status,management_kind,compose_project,compose_service,compose_working_dir,labels_json,ignored,protected,sensitive,manageable,safety_reason,last_sync FROM containers`

func scanContainer(scanner interface{ Scan(...any) error }) (domain.Container, error) {
	var c domain.Container
	var labels string
	var sync sql.NullString
	err := scanner.Scan(&c.ID, &c.AgentID, &c.DockerID, &c.Name, &c.Image, &c.Tag, &c.CurrentDigest, &c.RemoteDigest, &c.UpdateAvailable, &c.DetectionMethod, &c.RuntimeStatus, &c.ManagementKind, &c.ComposeProject, &c.ComposeService, &c.ComposeWorkingDir, &labels, &c.Ignored, &c.Protected, &c.Sensitive, &c.Manageable, &c.SafetyReason, &sync)
	if err != nil {
		return c, err
	}
	_ = json.Unmarshal([]byte(labels), &c.Labels)
	c.LastSync = parse(sync)
	return c, nil
}
func (s *Store) Containers(ctx context.Context, agentID string) ([]domain.Container, error) {
	q := containerSelect + " WHERE runtime_status<>'removed'"
	var args []any
	if agentID != "" {
		q += " AND agent_id=?"
		args = append(args, agentID)
	}
	q += " ORDER BY update_available DESC,name"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Container
	for rows.Next() {
		c, e := scanContainer(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) Container(ctx context.Context, id string) (domain.Container, error) {
	return scanContainer(s.db.QueryRowContext(ctx, containerSelect+" WHERE id=?", id))
}
func (s *Store) SetContainerFlag(ctx context.Context, id, flag string, v bool) error {
	if flag != "ignored" && flag != "protected" {
		return errors.New("invalid flag")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE containers SET "+flag+"=? WHERE id=?", v, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetStackFlag(ctx context.Context, agentID, project, flag string, v bool) error {
	if flag != "ignored" && flag != "protected" {
		return errors.New("invalid flag")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE containers SET "+flag+"=? WHERE agent_id=? AND compose_project=?", v, agentID, project)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateJob(ctx context.Context, j domain.Job) error {
	var containerID any
	if j.ContainerID != "" {
		containerID = j.ContainerID
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO update_jobs(id,agent_id,container_id,target_key,action,status,requested_by,correlation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, j.ID, j.AgentID, containerID, j.TargetKey, j.Action, j.Status, j.RequestedBy, j.CorrelationID, ts(j.CreatedAt))
	return err
}
func (s *Store) SetAgentJob(ctx context.Context, id, agentJob string) error {
	// The agent starts asynchronously and may report completion before the HTTP
	// response reaches us. Attach its ID without ever downgrading a terminal job.
	result, err := s.db.ExecContext(ctx, `UPDATE update_jobs SET agent_job_id=?,
		status=CASE WHEN status='queued' THEN 'running' ELSE status END,
		started_at=COALESCE(started_at,?) WHERE id=?`, agentJob, ts(time.Now()), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("job is missing")
	}
	return nil
}
func (s *Store) UpdateJob(ctx context.Context, id, status, plan, jobErr string) error {
	if status != domain.StatusRunning && status != domain.StatusSucceeded && status != domain.StatusFailed {
		return fmt.Errorf("invalid job status %q", status)
	}
	var finished any = nil
	if status == domain.StatusSucceeded || status == domain.StatusFailed {
		finished = ts(time.Now())
	}
	result, err := s.db.ExecContext(ctx, `UPDATE update_jobs SET status=?,dry_run_plan=CASE WHEN ?<>'' THEN ? ELSE dry_run_plan END,error=?,finished_at=?
		WHERE id=? AND ((status IN ('queued','running') AND ? IN ('running','succeeded','failed')) OR status=?)`, status, plan, plan, jobErr, finished, id, status, status)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("job is missing or has an invalid state transition")
	}
	return nil
}
func (s *Store) AddEvent(ctx context.Context, e domain.Event) (int64, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO update_events(job_id,level,message,created_at) VALUES(?,?,?,?)", e.JobID, e.Level, e.Message, ts(e.CreatedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const jobSelect = `SELECT id,agent_job_id,agent_id,COALESCE(container_id,''),target_key,action,status,requested_by,correlation_id,dry_run_plan,error,created_at,started_at,finished_at FROM update_jobs`

func scanJob(scanner interface{ Scan(...any) error }) (domain.Job, error) {
	var j domain.Job
	var cr, st, fn sql.NullString
	if err := scanner.Scan(&j.ID, &j.AgentJobID, &j.AgentID, &j.ContainerID, &j.TargetKey, &j.Action, &j.Status, &j.RequestedBy, &j.CorrelationID, &j.DryRunPlan, &j.Error, &cr, &st, &fn); err != nil {
		return j, err
	}
	c := parse(cr)
	j.CreatedAt = c
	if st.Valid {
		v := parse(st)
		j.StartedAt = &v
	}
	if fn.Valid {
		v := parse(fn)
		j.FinishedAt = &v
	}
	return j, nil
}
func (s *Store) Jobs(ctx context.Context, limit int) ([]domain.Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, jobSelect+" ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Job
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) Job(ctx context.Context, id string) (domain.Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, jobSelect+" WHERE id=?", id))
}
func (s *Store) Events(ctx context.Context, jobID string, after int64) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,job_id,level,message,created_at FROM update_events WHERE job_id=? AND id>? ORDER BY id", jobID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Event
	for rows.Next() {
		var e domain.Event
		var t sql.NullString
		if err := rows.Scan(&e.ID, &e.JobID, &e.Level, &e.Message, &t); err != nil {
			return nil, err
		}
		e.CreatedAt = parse(t)
		out = append(out, e)
	}
	return out, rows.Err()
}
