package store

import (
	"encoding/base64"
	"github.com/dockpulse/dockpulse/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestSensitiveOverridePersistsAndCannotBeSetByInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.db")
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	st, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := st.RegisterAgent(ctx, domain.Agent{ID: "a", Name: "host"}, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	yes := true
	inv := domain.Inventory{AgentID: "a", SyncedAt: time.Now(), Containers: []domain.Container{{DockerID: "old", Name: "db", RuntimeStatus: "running", Sensitive: true, SensitiveUpdatesAllowed: &yes}}}
	if err := st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	c, err := st.Container(ctx, "a:old")
	if err != nil || c.AllowsSensitiveUpdates() || c.SensitiveUpdatesAllowed != nil {
		t.Fatalf("inventory granted permission: %+v %v", c, err)
	}
	if err := st.SetContainerFlag(ctx, "a:old", "sensitive_updates_allowed", true); err != nil {
		t.Fatal(err)
	}
	inv.Containers[0].DockerID = "new"
	inv.Containers[0].SensitiveUpdatesAllowed = nil
	if err := st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	c, err = st.Container(ctx, "a:old")
	if err != nil || c.DockerID != "new" || !c.AllowsSensitiveUpdates() {
		t.Fatalf("recreation lost policy: %+v %v", c, err)
	}
	if err := st.SetContainerFlag(ctx, "a:old", "sensitive_updates_allowed", false); err != nil {
		t.Fatal(err)
	}
	inv.Containers[0].Labels = map[string]string{"io.dockpulse.allow-sensitive": "true"}
	inv.Containers[0].SensitiveUpdatesAllowed = &yes
	if err := st.UpsertInventory(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, err = st.Container(ctx, "a:old")
	if err != nil || c.AllowsSensitiveUpdates() || c.SensitiveUpdatesAllowed == nil {
		t.Fatalf("restart or inventory lost manager block: %+v %v", c, err)
	}
	if err := st.SetContainerFlag(ctx, "missing", "sensitive_updates_allowed", true); err == nil {
		t.Fatal("missing workload accepted")
	}
}
