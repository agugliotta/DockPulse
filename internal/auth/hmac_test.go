package auth

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func TestSignAndVerify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"action":"refresh"}`)
	req, _ := http.NewRequest("POST", "http://agent/refresh", bytes.NewReader(body))
	Sign(req, "agent-a", []byte("01234567890123456789012345678901"), body, now)
	got, err := Verify(req, []byte("01234567890123456789012345678901"), now)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body mismatch: %q", got)
	}
}
func TestVerifyRejectsTamperingAndExpiredRequests(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{}`)
	req, _ := http.NewRequest("POST", "http://agent/update", bytes.NewReader([]byte(`{"confirm":true}`)))
	Sign(req, "agent-a", []byte("01234567890123456789012345678901"), body, now)
	if _, err := Verify(req, []byte("01234567890123456789012345678901"), now); err == nil {
		t.Fatal("expected tampered body rejection")
	}
	req, _ = http.NewRequest("POST", "http://agent/update", bytes.NewReader(body))
	Sign(req, "agent-a", []byte("01234567890123456789012345678901"), body, now)
	if _, err := Verify(req, []byte("01234567890123456789012345678901"), now.Add(6*time.Minute)); err == nil {
		t.Fatal("expected expired request rejection")
	}
}

func TestReplayGuardRejectsSameNonce(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)
	secret := []byte("01234567890123456789012345678901")
	req, _ := http.NewRequest("POST", "http://agent/refresh", bytes.NewReader(body))
	Sign(req, "agent-a", secret, body, now)
	guard := NewReplayGuard()
	if _, err := guard.Verify(req, secret, now); err != nil {
		t.Fatal(err)
	}
	replay, _ := http.NewRequest("POST", "http://agent/refresh", bytes.NewReader(body))
	replay.Header = req.Header.Clone()
	if _, err := guard.Verify(replay, secret, now); err == nil {
		t.Fatal("expected replay rejection")
	}
}
