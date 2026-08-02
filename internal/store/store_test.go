package store

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/dockpulse/dockpulse/internal/domain"
)

func TestStoreInventoryPolicyAndTargetLock(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := Open(filepath.Join(t.TempDir(), "test.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	a := domain.Agent{ID: "a1", Name: "host-a", BaseURL: "http://agent:9090"}
	if err = st.RegisterAgent(ctx, a, []byte("abcdefghijklmnopqrstuvwxyz123456")); err != nil {
		t.Fatal(err)
	}
	secret, err := st.AgentSecret(ctx, "a1")
	if err != nil || string(secret) != "abcdefghijklmnopqrstuvwxyz123456" {
		t.Fatalf("secret roundtrip failed: %v", err)
	}
	inv := domain.Inventory{AgentID: "a1", SyncedAt: time.Now(), Containers: []domain.Container{{ID: "a1:c1", DockerID: "c1", Name: "web", Image: "nginx:1", RuntimeStatus: "running", ManagementKind: "docker-run", Manageable: true}}}
	if err = st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if err = st.SetContainerFlag(ctx, "a1:c1", "ignored", true); err != nil {
		t.Fatal(err)
	}
	c, err := st.Container(ctx, "a1:c1")
	if err != nil || !c.Ignored {
		t.Fatalf("policy not persisted: %+v %v", c, err)
	}
	j := domain.Job{ID: "j1", AgentID: "a1", ContainerID: "a1:c1", TargetKey: "a1:c1", Action: "update", Status: domain.StatusQueued, RequestedBy: "test", CorrelationID: "r1", CreatedAt: time.Now()}
	if err = st.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j.ID = "j2"
	if err = st.CreateJob(ctx, j); err == nil {
		t.Fatal("expected active-target lock")
	}
	if err = st.UpdateJob(ctx, "j1", domain.StatusSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = st.CreateJob(ctx, j); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestInventoryReconciliationRemovesStaleContainers(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := Open(filepath.Join(t.TempDir(), "reconcile.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err = st.RegisterAgent(ctx, domain.Agent{ID: "a1", Name: "host", BaseURL: "http://agent"}, []byte("abcdefghijklmnopqrstuvwxyz123456")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first := domain.Inventory{AgentID: "a1", SyncedAt: now, Containers: []domain.Container{{ID: "a1:old", DockerID: "old", Name: "old", Image: "nginx:1", RuntimeStatus: "running", ManagementKind: "docker-run"}}}
	if err = st.UpsertInventory(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := domain.Inventory{AgentID: "a1", SyncedAt: now.Add(time.Second), Containers: []domain.Container{{ID: "a1:new", DockerID: "new", Name: "new", Image: "nginx:2", RuntimeStatus: "running", ManagementKind: "docker-run"}}}
	if err = st.UpsertInventory(ctx, second); err != nil {
		t.Fatal(err)
	}
	containers, err := st.Containers(ctx, "a1")
	if err != nil || len(containers) != 1 || containers[0].DockerID != "new" {
		t.Fatalf("stale inventory was not reconciled: %+v %v", containers, err)
	}
}

func TestInventoryRecreationPreservesControlIdentityAndPolicy(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := Open(filepath.Join(t.TempDir(), "identity.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err = st.RegisterAgent(ctx, domain.Agent{ID: "a1", Name: "host", BaseURL: "http://agent"}, []byte("abcdefghijklmnopqrstuvwxyz123456")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = st.UpsertInventory(ctx, domain.Inventory{AgentID: "a1", SyncedAt: now, Containers: []domain.Container{{ID: "a1:old", DockerID: "old", Name: "web", Image: "nginx:1", RuntimeStatus: "running"}}}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetContainerFlag(ctx, "a1:old", "protected", true); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertInventory(ctx, domain.Inventory{AgentID: "a1", SyncedAt: now.Add(time.Minute), Containers: []domain.Container{{ID: "a1:new", DockerID: "new", Name: "web", Image: "nginx:2", RuntimeStatus: "running"}}}); err != nil {
		t.Fatal(err)
	}
	c, err := st.Container(ctx, "a1:old")
	if err != nil || c.DockerID != "new" || c.Image != "nginx:2" || !c.Protected {
		t.Fatalf("identity or policy was not preserved: %+v %v", c, err)
	}
}

func TestSetContainerFlagRejectsMissingContainer(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := Open(filepath.Join(t.TempDir(), "missing.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.SetContainerFlag(context.Background(), "missing", "ignored", true); err == nil {
		t.Fatal("expected a missing-container error")
	}
}

func TestInventorySnapshotsOnlyRecordStateTransitions(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := Open(filepath.Join(t.TempDir(), "snapshots.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err = st.RegisterAgent(ctx, domain.Agent{ID: "a1", Name: "host", BaseURL: "http://agent"}, []byte("abcdefghijklmnopqrstuvwxyz123456")); err != nil {
		t.Fatal(err)
	}
	inv := domain.Inventory{AgentID: "a1", SyncedAt: time.Now(), Containers: []domain.Container{{ID: "a1:c1", DockerID: "c1", Name: "web", Image: "nginx:1", CurrentDigest: "sha256:a", RuntimeStatus: "running"}}}
	for i := 0; i < 2; i++ {
		if err = st.UpsertInventory(ctx, inv); err != nil {
			t.Fatal(err)
		}
	}
	inv.Containers[0].CurrentDigest = "sha256:b"
	if err = st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	inv.Containers[0].CurrentDigest = "sha256:a"
	if err = st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM image_snapshots").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected one snapshot per state transition, got %d", count)
	}
}

func TestSetAgentJobDoesNotDowngradeFastTerminalJob(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	st, err := Open(filepath.Join(t.TempDir(), "job-race.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err = st.RegisterAgent(ctx, domain.Agent{ID: "a1", Name: "host", BaseURL: "http://agent"}, []byte("abcdefghijklmnopqrstuvwxyz123456")); err != nil {
		t.Fatal(err)
	}
	j := domain.Job{ID: "j1", AgentID: "a1", TargetKey: "a1:c1", Action: "update", Status: domain.StatusQueued, RequestedBy: "test", CreatedAt: time.Now()}
	if err = st.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err = st.UpdateJob(ctx, j.ID, domain.StatusSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = st.SetAgentJob(ctx, j.ID, "agent-fast"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Job(ctx, j.ID)
	if err != nil || got.Status != domain.StatusSucceeded || got.AgentJobID != "agent-fast" {
		t.Fatalf("terminal job was downgraded: %+v %v", got, err)
	}
}
