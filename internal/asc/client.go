// Package asc is a thin client for the App Store Connect API, scoped to the
// data the exporter needs: sales reports, subscription reports and reviews.
package asc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BaseURL is the App Store Connect API root.
const BaseURL = "https://api.appstoreconnect.apple.com"

// Client talks to the App Store Connect API using a TokenSource for auth.
type Client struct {
	http    *http.Client
	ts      *TokenSource
	baseURL string
}

// New returns a Client. timeout bounds each HTTP request.
func New(ts *TokenSource, timeout time.Duration) *Client {
	return &Client{
		http:    &http.Client{Timeout: timeout},
		ts:      ts,
		baseURL: BaseURL,
	}
}

// apiError is Apple's standard error envelope.
type apiError struct {
	Errors []struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"errors"`
}

func (e apiError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, er := range e.Errors {
		parts = append(parts, fmt.Sprintf("%s: %s", er.Title, er.Detail))
	}
	return strings.Join(parts, "; ")
}

// get performs an authenticated GET. accept overrides the Accept header.
// The caller owns the returned body and must close it.
func (c *Client) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	tok, err := c.ts.Token()
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return c.http.Do(req)
}

// statusError reads an error body and wraps it with the HTTP status.
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var ae apiError
	if json.Unmarshal(body, &ae) == nil && len(ae.Errors) > 0 {
		return fmt.Errorf("ASC API %d: %w", resp.StatusCode, ae)
	}
	return fmt.Errorf("ASC API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
