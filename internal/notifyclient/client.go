package notifyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DispatchRequest mirrors iag-notifications/internal/domain.DispatchRequest.
// Fields use the JSON tags expected by the central service.
type DispatchRequest struct {
	Channel       string            `json:"channel"`
	Recipient     string            `json:"recipient"`
	TemplateID    string            `json:"templateId"`
	Variables     map[string]string `json:"variables,omitempty"`
	EventID       string            `json:"eventId,omitempty"`
	CorrelationID string            `json:"correlationId,omitempty"`
	CausationID   string            `json:"causationId,omitempty"`
}

// DispatchResult mirrors the central service's response. Fields that are
// not used by procurement are still decoded so future use does not
// require a coordinated change.
type DispatchResult struct {
	Status      string `json:"status"`
	DeliveryID  string `json:"deliveryId,omitempty"`
	Message     string `json:"message,omitempty"`
	ProviderRef string `json:"providerRef,omitempty"`
}

// Dispatcher is the boundary between the notifications service code
// and the transport. Two implementations live in this package: HTTP
// (real) and Noop (logs only — used when notifications is not
// configured, so local dev stays runnable without the auth + notify
// dependency chain).
type Dispatcher interface {
	Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error)
}

// Client is the HTTP dispatcher. It uses a ServiceAuth to mint a fresh
// Bearer token for each request (the token is cached and re-used until
// near expiry by ServiceAuth itself).
type Client struct {
	baseURL string
	auth    *ServiceAuth
	http    *http.Client
}

// NewClient builds the HTTP dispatcher. baseURL is the root of the
// notifications service (e.g. http://notifications:3002), without the
// /v1/dispatch suffix.
func NewClient(baseURL string, auth *ServiceAuth) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		auth:    auth,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Dispatch POSTs the request to /v1/dispatch with a Bearer token.
// Non-2xx responses return an error containing the response body for
// debuggability. Notifications dedups by (eventId, channel, recipient)
// so callers can retry safely as long as eventId is stable.
func (c *Client) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("notifyclient: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/dispatch", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("notifyclient: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	tok, err := c.auth.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("notifyclient: auth: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("notifyclient: post: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("notifyclient: dispatch %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out DispatchResult
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("notifyclient: decode response: %w", err)
		}
	}
	return &out, nil
}

// Noop is the dispatcher used when NOTIFICATIONS_URL is unset. It logs
// each request at INFO so dev environments still see notification
// intent without needing the auth + notifications services running.
type Noop struct{}

func (Noop) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
	slog.InfoContext(ctx, "notifyclient: noop dispatch",
		"channel", req.Channel,
		"recipient", req.Recipient,
		"templateId", req.TemplateID,
		"eventId", req.EventID,
	)
	return &DispatchResult{Status: "noop"}, nil
}
