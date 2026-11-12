// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client talks to syncd's documented HTTP interface. It never opens the core
// database; the queue and the upload endpoint are the only seams.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client with a bounded request timeout. Uploads of a
// single track fit comfortably inside it.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Token:   strings.TrimSpace(token),
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// Queued returns the requests waiting for a worker, oldest first.
func (c *Client) Queued(ctx context.Context) ([]Job, error) {
	resp, err := c.do(ctx, http.MethodGet, "/sync/v1/acquisition?state=queued", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("list queued", resp)
	}
	var payload struct {
		Requests []Job `json:"requests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("list queued: decode: %w", err)
	}
	return payload.Requests, nil
}

// Claim takes one queued request. A disabled worker maps to ErrStopped.
func (c *Client) Claim(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/sync/v1/acquisition/"+url.PathEscape(id)+"/claim", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if strings.Contains(strings.ToLower(string(body)), "disabled") {
			return ErrStopped
		}
		return responseError("claim", resp)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError("claim", resp)
	}
	return nil
}

// Complete marks a claimed request done.
func (c *Client) Complete(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/sync/v1/acquisition/"+url.PathEscape(id)+"/complete", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError("complete", resp)
	}
	return nil
}

// Fail records a worker error on a claimed request.
func (c *Client) Fail(ctx context.Context, id, message string) error {
	body, err := json.Marshal(map[string]string{"error": message})
	if err != nil {
		return fmt.Errorf("fail: encode: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/sync/v1/acquisition/"+url.PathEscape(id)+"/fail", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError("fail", resp)
	}
	return nil
}

// AddReview records a match that needs a human decision. It uses the core
// review queue, not a private table.
func (c *Client) AddReview(ctx context.Context, job Job, candidate Candidate, reason string) error {
	body, err := json.Marshal(map[string]string{
		"canonical_id": job.CanonicalID,
		"provider":     "yt-dlp",
		"service_id":   candidate.ID,
		"reason":       reason,
	})
	if err != nil {
		return fmt.Errorf("review: encode: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/sync/v1/review", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return responseError("review", resp)
	}
	return nil
}

// Ingest uploads a downloaded file as a cached rendition for the requested
// canonical track. The core verifies the digest before indexing it.
func (c *Client) Ingest(ctx context.Context, job Job, path, hash, name string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("ingest: open download: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("ingest: stat download: %w", err)
	}
	if info.Size() <= 0 {
		return fmt.Errorf("ingest: download is empty")
	}

	query := url.Values{}
	query.Set("name", name)
	query.Set("track", job.CanonicalID)
	query.Set("kind", "cached")
	target := c.BaseURL + "/sync/v1/library/uploads/" + url.PathEscape(strings.ToLower(hash)) + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, file)
	if err != nil {
		return fmt.Errorf("ingest: build request: %w", err)
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", info.Size()-1, info.Size()))
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("ingest: upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return responseError("ingest", resp)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: build request: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return resp, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func responseError(operation string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("%s: status %d: %s", operation, resp.StatusCode, message)
}
