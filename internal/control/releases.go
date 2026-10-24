package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

type releaseInfo struct {
	Version   string    `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

var stableReleaseTag = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

func (s *Server) latestRelease(ctx context.Context) releaseInfo {
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	if time.Now().Before(s.releaseExpires) {
		return s.release
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info := releaseInfo{CheckedAt: time.Now().UTC()}
	version, err := s.fetchLatestRelease(ctx)
	ttl := 5 * time.Minute
	if err != nil {
		info.Error = "Could not check the latest release. Try again shortly."
		ttl = 30 * time.Second
	} else {
		info.Version = version
	}
	s.release, s.releaseExpires = info, time.Now().Add(ttl)
	return info
}

func (s *Server) fetchLatestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/agugliotta/DockPulse/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DockPulse-Control")
	res, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release lookup: HTTP %d", res.StatusCode)
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&release); err != nil {
		return "", err
	}
	if release.Draft || release.Prerelease || !stableReleaseTag.MatchString(release.Tag) {
		return "", fmt.Errorf("release lookup: invalid stable release")
	}
	return release.Tag, nil
}
