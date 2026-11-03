package store

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/dockpulse/dockpulse/internal/domain"
)

func TestRegistrationRequiresExistingIdentitySecret(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "enrollment.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secret := []byte("original-secret")
	a := domain.Agent{ID: "a", Name: "original", BaseURL: "http://original", ReadOnly: true}
	if err := st.RegisterAgent(t.Context(), a, secret); err != nil {
		t.Fatal(err)
	}
	forged := a
	forged.Name = "forged"
	forged.BaseURL = "http://forged"
	forged.ReadOnly = false
	if err := st.RegisterAgent(t.Context(), forged, []byte("different-secret")); err == nil {
		t.Fatal("identity replaced without its current secret")
	}
	got, err := st.Agent(t.Context(), a.ID)
	if err != nil || got.Name != a.Name || got.BaseURL != a.BaseURL || !got.ReadOnly {
		t.Fatalf("registration changed metadata: %+v %v", got, err)
	}
	stored, err := st.AgentSecret(t.Context(), a.ID)
	if err != nil || !bytes.Equal(stored, secret) {
		t.Fatal("original secret changed")
	}
	forged.Name = "renamed"
	if err := st.RegisterAgent(t.Context(), forged, secret); err != nil {
		t.Fatalf("legitimate re-registration blocked: %v", err)
	}
}

func TestInventoryGapPreservesPolicies(t *testing.T) {
	for _, flag := range []string{"ignored", "protected", "sensitive_updates_allowed"} {
		t.Run(flag, func(t *testing.T) {
			st, err := Open(filepath.Join(t.TempDir(), "review.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.RegisterAgent(t.Context(), domain.Agent{ID: "a", Name: "host"}, []byte("secret")); err != nil {
				t.Fatal(err)
			}
			c := domain.Container{DockerID: "old", Name: "db", Image: "postgres:17", RuntimeStatus: "running", Sensitive: true, Labels: map[string]string{"io.dockpulse.allow-sensitive": "true"}}
			syncInv := func(cs []domain.Container) {
				t.Helper()
				if err := st.UpsertInventory(t.Context(), domain.Inventory{AgentID: "a", SyncedAt: time.Now(), Containers: cs}); err != nil {
					t.Fatal(err)
				}
			}
			syncInv([]domain.Container{c})
			if err := st.SetContainerFlag(t.Context(), "a:old", flag, flag != "sensitive_updates_allowed"); err != nil {
				t.Fatal(err)
			}
			syncInv(nil)
			c.DockerID = "new"
			syncInv([]domain.Container{c})
			got, err := st.Container(t.Context(), "a:old")
			if err != nil || got.DockerID != "new" {
				t.Fatalf("logical identity lost: %+v %v", got, err)
			}
			if flag == "ignored" && !got.Ignored || flag == "protected" && !got.Protected || flag == "sensitive_updates_allowed" && (got.SensitiveUpdatesAllowed == nil || got.AllowsSensitiveUpdates()) {
				t.Fatalf("policy %s lost: %+v", flag, got)
			}
		})
	}
}

func TestBackupInventoryPreservesReplacementPolicy(t *testing.T) {
	for _, newFirst := range []bool{true, false} {
		st, err := Open(filepath.Join(t.TempDir(), "review.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
		if err != nil {
			t.Fatal(err)
		}
		if err = st.RegisterAgent(t.Context(), domain.Agent{ID: "a", Name: "host"}, []byte("secret")); err != nil {
			t.Fatal(err)
		}
		old := domain.Container{DockerID: "old", Name: "db", Image: "postgres:17", RuntimeStatus: "running", Sensitive: true, Labels: map[string]string{"io.dockpulse.allow-sensitive": "true"}}
		syncInv := func(cs []domain.Container) {
			t.Helper()
			if err := st.UpsertInventory(t.Context(), domain.Inventory{AgentID: "a", SyncedAt: time.Now(), Containers: cs}); err != nil {
				t.Fatal(err)
			}
		}
		syncInv([]domain.Container{old})
		if err = st.SetContainerFlag(t.Context(), "a:old", "sensitive_updates_allowed", false); err != nil {
			t.Fatal(err)
		}
		if err = st.CreateJob(t.Context(), domain.Job{ID: "job", AgentID: "a", ContainerID: "a:old", TargetKey: "a:old", Action: "dry-run", Status: domain.StatusSucceeded, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		backup := old
		backup.Name = "db.dockpulse-backup"
		backup.RuntimeStatus = "exited"
		replacement := old
		replacement.DockerID = "new"
		if newFirst {
			syncInv([]domain.Container{replacement, backup})
		} else {
			syncInv([]domain.Container{backup, replacement})
		}
		syncInv([]domain.Container{replacement})
		got, err := st.Container(t.Context(), "a:old")
		if err != nil {
			t.Fatal(err)
		}
		if got.SensitiveUpdatesAllowed == nil || got.AllowsSensitiveUpdates() || got.DockerID != "new" {
			t.Fatalf("regression: %+v", got)
		}
		referenced, err := st.Container(t.Context(), "a:old")
		if err != nil || referenced.Name != "db" {
			t.Fatalf("audit reference changed: %+v %v", referenced, err)
		}
		st.Close()
	}
}
