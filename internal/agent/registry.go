package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type RegistryClient struct{ Client *http.Client }

func NewRegistryClient() *RegistryClient {
	return &RegistryClient{Client: &http.Client{Timeout: 12 * time.Second}}
}

func parseReference(ref string) (repo, tag, registry string) {
	ref = strings.SplitN(ref, "@", 2)[0]
	first := ""
	if i := strings.Index(ref, "/"); i >= 0 {
		first = ref[:i]
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		registry = first
		repo = strings.TrimPrefix(ref, first+"/")
	} else {
		registry = "registry-1.docker.io"
		repo = ref
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
	}
	slash := strings.LastIndex(repo, "/")
	colon := strings.LastIndex(repo, ":")
	tag = "latest"
	if colon > slash {
		tag = repo[colon+1:]
		repo = repo[:colon]
	}
	return
}

func (r *RegistryClient) Digest(ctx context.Context, ref string) (string, error) {
	repo, tag, registry := parseReference(ref)
	scheme := "https"
	if strings.HasPrefix(registry, "localhost") || strings.HasPrefix(registry, "127.0.0.1") {
		scheme = "http"
	}
	endpoint := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repo, url.PathEscape(tag))
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	accept(req)
	res, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		challenge := res.Header.Get("WWW-Authenticate")
		token, err := r.token(ctx, challenge)
		if err != nil {
			return "", err
		}
		req, _ = http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
		accept(req)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err = r.Client.Do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return "", fmt.Errorf("registry returned %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	d := res.Header.Get("Docker-Content-Digest")
	if d == "" {
		return "", fmt.Errorf("registry did not return a manifest digest")
	}
	return d, nil
}
func accept(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
}

var bearerRE = regexp.MustCompile(`([a-z]+)="([^"]+)"`)

func (r *RegistryClient) token(ctx context.Context, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", fmt.Errorf("unsupported registry authentication")
	}
	vals := map[string]string{}
	for _, m := range bearerRE.FindAllStringSubmatch(challenge, -1) {
		vals[m[1]] = m[2]
	}
	realm := vals["realm"]
	if realm == "" {
		return "", fmt.Errorf("invalid registry challenge")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for _, k := range []string{"service", "scope"} {
		if vals[k] != "" {
			q.Set(k, vals[k])
		}
	}
	u.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	res, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("registry token service returned %d", res.StatusCode)
	}
	var v struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&v); err != nil {
		return "", err
	}
	if v.Token == "" {
		v.Token = v.AccessToken
	}
	if v.Token == "" {
		return "", fmt.Errorf("registry token missing")
	}
	return v.Token, nil
}
