package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dockpulse/dockpulse/internal/domain"
)

func TestSensitivePolicyExecution(t *testing.T) {
	for _, tc := range []struct {
		name              string
		policy            map[string]bool
		disabled, allowed bool
	}{
		{name: "default blocked"},
		{name: "manager allowed", policy: map[string]bool{"db": true}, allowed: true},
		{name: "wrong service", policy: map[string]bool{"other": true}},
		{name: "explicit block", policy: map[string]bool{"db": false}},
		{name: "management disabled still blocked", policy: map[string]bool{"db": true}, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			in := inspectContainer{}
			in.ID, in.Name = "db-id", "/database"
			in.Config.Image = "postgres:17"
			in.Config.Labels = map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "db", "com.docker.compose.project.working_dir": dir}
			if tc.disabled {
				in.Config.Labels["io.dockpulse.manage"] = "false"
			}
			raw, _ := json.Marshal([]inspectContainer{in})
			mutations := 0
			u := Updater{AllowedComposeRoots: []string{dir}, Docker: runFunc(func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == "inspect" {
					return raw, nil
				}
				if strings.Contains(strings.Join(args, " "), "config --format json") {
					return []byte(`{"services":{"db":{"image":"postgres:17"}}}`), nil
				}
				mutations++
				return nil, nil
			})}
			err := u.Execute(t.Context(), domain.ActionRequest{ContainerID: "db-id", Scope: "service", Confirm: true, SensitivePolicy: tc.policy}, func(string, string) {})
			if (err == nil) != tc.allowed {
				t.Fatalf("unexpected execution result: %v", err)
			}
			if !tc.allowed && mutations != 0 {
				t.Fatal("blocked policy mutated Docker")
			}
			if tc.allowed && mutations != 2 {
				t.Fatalf("expected pull/up, got %d mutations", mutations)
			}
		})
	}
}

func TestSensitivePolicyCannotAuthorizeOtherStackServices(t *testing.T) {
	dir := t.TempDir()
	in := inspectContainer{}
	in.Config.Labels = map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "db", "com.docker.compose.project.working_dir": dir}
	u := Updater{AllowedComposeRoots: []string{dir}, Docker: runFunc(func(context.Context, ...string) ([]byte, error) {
		return []byte(`{"services":{"db":{"image":"postgres:17"},"cache":{"image":"redis:8"}}}`), nil
	})}
	if _, err := u.composePlan(t.Context(), in, "stack", map[string]bool{"db": true}); err == nil {
		t.Fatal("permission for db also authorized cache")
	}
	if _, err := u.composePlan(t.Context(), in, "stack", map[string]bool{"db": true, "cache": true}); err != nil {
		t.Fatal(err)
	}
}

func TestDockerRunSensitiveOverride(t *testing.T) {
	in := inspectContainer{}
	in.ID, in.Config.Image = "db-id", "postgres:17"
	in.Config.Labels = map[string]string{"io.dockpulse.manage": "true", "io.dockpulse.allow-sensitive": "true"}
	if err := validateManaged(in, nil); err != nil {
		t.Fatal("legacy label no longer works", err)
	}
	if err := validateManaged(in, map[string]bool{"db-id": false}); err == nil {
		t.Fatal("manager block did not override label")
	}
	delete(in.Config.Labels, "io.dockpulse.allow-sensitive")
	if err := validateManaged(in, map[string]bool{"another-id": true}); err == nil {
		t.Fatal("another container's permission was accepted")
	}
	if err := validateManaged(in, map[string]bool{"db-id": true}); err != nil {
		t.Fatal(err)
	}
	in.Config.Labels["io.dockpulse.manage"] = "false"
	if err := validateManaged(in, map[string]bool{"db-id": true}); err == nil {
		t.Fatal("manager permission bypassed management opt-out")
	}
}
