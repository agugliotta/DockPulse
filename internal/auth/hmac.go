package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	HeaderAgent   = "X-DockPulse-Agent"
	HeaderTime    = "X-DockPulse-Timestamp"
	HeaderSig     = "X-DockPulse-Signature"
	HeaderNonce   = "X-DockPulse-Nonce"
	HeaderRequest = "X-Request-ID"
	MaxBodyBytes  = 2 << 20
)

func bodyHash(body []byte) string {
	s := sha256.Sum256(body)
	return hex.EncodeToString(s[:])
}

func signature(secret []byte, method, path, timestamp, nonce string, body []byte) string {
	m := hmac.New(sha256.New, secret)
	fmt.Fprintf(m, "%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, bodyHash(body))
	return hex.EncodeToString(m.Sum(nil))
}

func Sign(req *http.Request, agentID string, secret, body []byte, now time.Time) {
	ts := strconv.FormatInt(now.Unix(), 10)
	nonceBytes := make([]byte, 16)
	_, _ = rand.Read(nonceBytes)
	nonce := hex.EncodeToString(nonceBytes)
	req.Header.Set(HeaderAgent, agentID)
	req.Header.Set(HeaderTime, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSig, signature(secret, req.Method, req.URL.RequestURI(), ts, nonce, body))
}

func Verify(req *http.Request, secret []byte, now time.Time) ([]byte, error) {
	ts := req.Header.Get(HeaderTime)
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || abs(now.Unix()-unix) > 300 {
		return nil, fmt.Errorf("expired or invalid timestamp")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, MaxBodyBytes+1))
	if err != nil || len(body) > MaxBodyBytes {
		return nil, fmt.Errorf("invalid request body")
	}
	nonce := req.Header.Get(HeaderNonce)
	if len(nonce) != 32 {
		return nil, fmt.Errorf("invalid nonce")
	}
	expected := signature(secret, req.Method, req.URL.RequestURI(), ts, nonce, body)
	provided := strings.ToLower(req.Header.Get(HeaderSig))
	if !hmac.Equal([]byte(expected), []byte(provided)) {
		return nil, fmt.Errorf("invalid signature")
	}
	return body, nil
}

type ReplayGuard struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func NewReplayGuard() *ReplayGuard { return &ReplayGuard{seen: make(map[string]time.Time)} }

func (g *ReplayGuard) Verify(req *http.Request, secret []byte, now time.Time) ([]byte, error) {
	body, err := Verify(req, secret, now)
	if err != nil {
		return nil, err
	}
	key := req.Header.Get(HeaderAgent) + ":" + req.Header.Get(HeaderNonce)
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, seenAt := range g.seen {
		if now.Sub(seenAt) > 5*time.Minute {
			delete(g.seen, k)
		}
	}
	if _, ok := g.seen[key]; ok {
		return nil, fmt.Errorf("replayed request")
	}
	g.seen[key] = now
	return body, nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
