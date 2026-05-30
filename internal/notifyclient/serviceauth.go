// Package notifyclient sends notification dispatch requests to the
// central iag-notifications service. It owns a small OAuth2
// client_credentials client (inlined from
// shared/platform-go/serviceauth so procurement does not need to
// vendor the platform module) and an HTTP wrapper that POSTs
// DispatchRequests to /v1/dispatch with a fresh Bearer token.
package notifyclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ServiceAuthOptions configures the cached client_credentials token client.
type ServiceAuthOptions struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Audience     string
	EarlyRefresh time.Duration
	HTTPClient   *http.Client
}

// ServiceAuth fetches and caches OAuth2 client_credentials access tokens.
type ServiceAuth struct {
	opts   ServiceAuthOptions
	http   *http.Client
	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewServiceAuth panics if required fields are missing; callers must
// validate options before construction.
func NewServiceAuth(opts ServiceAuthOptions) *ServiceAuth {
	if opts.TokenURL == "" || opts.ClientID == "" || opts.ClientSecret == "" {
		panic("notifyclient.NewServiceAuth: TokenURL, ClientID, and ClientSecret are required")
	}
	if opts.EarlyRefresh <= 0 {
		opts.EarlyRefresh = 60 * time.Second
	}
	httpC := opts.HTTPClient
	if httpC == nil {
		httpC = &http.Client{Timeout: 10 * time.Second}
	}
	return &ServiceAuth{opts: opts, http: httpC}
}

// Token returns a valid access token, refreshing if needed.
func (a *ServiceAuth) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Until(a.expiry) > a.opts.EarlyRefresh {
		return a.token, nil
	}
	if err := a.fetchLocked(ctx); err != nil {
		return "", err
	}
	return a.token, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

func (a *ServiceAuth) fetchLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", a.opts.ClientID)
	form.Set("client_secret", a.opts.ClientSecret)
	if a.opts.Audience != "" {
		form.Set("audience", a.opts.Audience)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.opts.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("notifyclient: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("notifyclient: post token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notifyclient: token endpoint %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("notifyclient: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return errors.New("notifyclient: empty access_token in response")
	}
	a.token = tr.AccessToken
	a.expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return nil
}
