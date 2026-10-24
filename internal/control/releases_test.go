package control

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLatestRelease(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"stable", `{"tag_name":"v0.3.5"}`, "v0.3.5", 200},
		{"unprefixed", `{"tag_name":"0.3.5"}`, "0.3.5", 200},
		{"rate limited", `{}`, "", 403},
		{"unavailable", `{}`, "", 503},
		{"malformed", `not json`, "", 200},
		{"invalid tag", `{"tag_name":"latest"}`, "", 200},
		{"prerelease", `{"tag_name":"v0.4.0","prerelease":true}`, "", 200},
		{"draft", `{"tag_name":"v0.4.0","draft":true}`, "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := &Server{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://api.github.com/repos/agugliotta/DockPulse/releases/latest" {
					t.Fatalf("unexpected URL: %s", r.URL)
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("missing bounded timeout")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			for i := 0; i < 2; i++ {
				got := s.latestRelease(t.Context())
				if got.Version != tc.want || (got.Error != "") != (tc.want == "") || got.CheckedAt.IsZero() {
					t.Fatalf("unexpected release: %+v", got)
				}
			}
			if calls != 1 {
				t.Fatalf("lookup not cached: %d calls", calls)
			}
			s.releaseExpires = time.Now().Add(-time.Second)
			s.latestRelease(t.Context())
			if calls != 2 {
				t.Fatal("expired cache was not refreshed")
			}
		})
	}
}

func TestLatestReleaseFailureDoesNotReportStaleVersion(t *testing.T) {
	s := &Server{
		release: releaseInfo{Version: "v0.3.5"},
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network unavailable: %w", context.DeadlineExceeded)
		})},
	}
	got := s.latestRelease(t.Context())
	if got.Version != "" || got.Error == "" {
		t.Fatalf("failed lookup reported a version: %+v", got)
	}
}
