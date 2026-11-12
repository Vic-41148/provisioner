// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

//go:build integration

// Package integration drains a queue through the real client and runner. The
// core is an httptest server; no YouTube, network or yt-dlp is involved.
package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vic-41148/provisioner/internal/acquisition"
)

type fakeCore struct {
	mu        sync.Mutex
	jobs      map[string]*acquisition.Job
	order     []string
	claimed   []string
	completed []string
	failed    []string
	uploads   map[string]string
	reviews   []map[string]string
	disabled  bool
}

func newFakeCore() *fakeCore {
	return &fakeCore{jobs: map[string]*acquisition.Job{}, uploads: map[string]string{}}
}

func (c *fakeCore) add(id, canonicalID, query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs[id] = &acquisition.Job{ID: id, CanonicalID: canonicalID, Query: query, State: "queued"}
	c.order = append(c.order, id)
}

func (c *fakeCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/sync/v1/acquisition":
		requests := make([]acquisition.Job, 0, len(c.order))
		for _, id := range c.order {
			job := c.jobs[id]
			if job.State == "queued" {
				requests = append(requests, *job)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"requests": requests})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/sync/v1/acquisition/"):
		c.handleAction(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/sync/v1/library/uploads/"):
		c.handleUpload(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/sync/v1/review":
		c.handleReview(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (c *fakeCore) handleReview(w http.ResponseWriter, r *http.Request) {
	var payload map[string]string
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "decode body"})
		return
	}
	for _, key := range []string{"provider", "service_id", "reason"} {
		if strings.TrimSpace(payload[key]) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": key + " required"})
			return
		}
	}
	c.reviews = append(c.reviews, payload)
	writeJSON(w, http.StatusCreated, map[string]string{"id": "review-1"})
}

func (c *fakeCore) handleAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	id, action := parts[3], parts[4]
	job, ok := c.jobs[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown request"})
		return
	}

	switch action {
	case "claim":
		if c.disabled {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "worker disabled"})
			return
		}
		if job.State != "queued" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "not queued"})
			return
		}
		job.State = "claimed"
		c.claimed = append(c.claimed, id)
		w.WriteHeader(http.StatusNoContent)
	case "complete":
		job.State = "done"
		c.completed = append(c.completed, id)
		w.WriteHeader(http.StatusNoContent)
	case "fail":
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		job.State = "queued"
		job.LastError = payload.Error
		job.Attempts++
		c.failed = append(c.failed, id)
		writeJSON(w, http.StatusOK, map[string]string{"state": job.State})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (c *fakeCore) handleUpload(w http.ResponseWriter, r *http.Request) {
	hash := filepath.Base(r.URL.Path)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
		return
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != strings.ToLower(hash) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "digest mismatch"})
		return
	}
	track := r.URL.Query().Get("track")
	if track == "" || r.URL.Query().Get("kind") != "cached" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad upload query"})
		return
	}
	c.uploads[track] = hash
	w.WriteHeader(http.StatusCreated)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type fakeProvider struct {
	dir     string
	content string
	err     error
	calls   []acquisition.Request
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Acquire(_ context.Context, request acquisition.Request) (string, error) {
	p.calls = append(p.calls, request)
	if p.err != nil {
		return "", p.err
	}
	path := filepath.Join(p.dir, request.ID+".opus")
	if err := os.WriteFile(path, []byte(p.content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (p *fakeProvider) Health(context.Context) (acquisition.Health, error) {
	return acquisition.Health{Available: true}, nil
}

func newRunner(t *testing.T, core *fakeCore, provider acquisition.Provider, between time.Duration) (acquisition.Runner, *[]time.Duration, *acquisition.Client) {
	t.Helper()
	server := httptest.NewServer(core)
	t.Cleanup(server.Close)
	client := acquisition.NewClient(server.URL, "device-token")

	delays := &[]time.Duration{}
	return acquisition.Runner{
		Queue:    client,
		Provider: provider,
		Ingest:   client,
		Between:  between,
		Sleep: func(d time.Duration) {
			*delays = append(*delays, d)
		},
	}, delays, client
}

func TestProvisionerDrainsQueueWithFakeYtDlp(t *testing.T) {
	core := newFakeCore()
	core.add("req-1", "track-1", "Artist Song")
	provider := &fakeProvider{dir: t.TempDir(), content: "downloaded audio"}
	runner, _, _ := newRunner(t, core, provider, time.Second)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(core.claimed) != 1 || core.claimed[0] != "req-1" {
		t.Fatalf("claimed = %#v, want req-1", core.claimed)
	}
	if len(core.completed) != 1 || core.completed[0] != "req-1" {
		t.Fatalf("completed = %#v, want req-1", core.completed)
	}
	if core.jobs["req-1"].State != "done" {
		t.Fatalf("state = %q, want done", core.jobs["req-1"].State)
	}
	sum := sha256.Sum256([]byte(provider.content))
	if got, want := core.uploads["track-1"], hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("upload hash = %q, want %q", got, want)
	}
}

func TestProvisionerRecordsProviderFailure(t *testing.T) {
	core := newFakeCore()
	core.add("req-1", "track-1", "Artist Song")
	provider := &fakeProvider{dir: t.TempDir(), err: errors.New("download refused")}
	runner, _, _ := newRunner(t, core, provider, time.Second)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(core.failed) != 1 || core.failed[0] != "req-1" {
		t.Fatalf("failed = %#v, want req-1", core.failed)
	}
	if len(core.completed) != 0 {
		t.Fatalf("completed = %#v, want none", core.completed)
	}
	if got := core.jobs["req-1"].LastError; got != "download refused" {
		t.Fatalf("last_error = %q, want download refused", got)
	}
	if core.jobs["req-1"].State != "queued" {
		t.Fatalf("state = %q, want queued for retry", core.jobs["req-1"].State)
	}
}

func TestProvisionerStopsOnKillSwitch(t *testing.T) {
	core := newFakeCore()
	core.add("req-1", "track-1", "Artist Song")
	core.add("req-2", "track-2", "Artist Other")
	core.disabled = true
	provider := &fakeProvider{dir: t.TempDir(), content: "audio"}
	runner, _, _ := newRunner(t, core, provider, time.Second)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("provider calls = %d, want none after the first refusal", len(provider.calls))
	}
	if len(core.claimed) != 0 || len(core.completed) != 0 {
		t.Fatalf("claimed = %#v completed = %#v, want none", core.claimed, core.completed)
	}
}

func TestProvisionerSpacesDownloads(t *testing.T) {
	core := newFakeCore()
	core.add("req-1", "track-1", "Artist Song")
	core.add("req-2", "track-2", "Artist Other")
	provider := &fakeProvider{dir: t.TempDir(), content: "audio"}
	runner, delays, _ := newRunner(t, core, provider, 250*time.Millisecond)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(*delays) != 1 || (*delays)[0] != 250*time.Millisecond {
		t.Fatalf("delays = %#v, want one 250ms pause", *delays)
	}
	if len(core.completed) != 2 {
		t.Fatalf("completed = %#v, want both jobs", core.completed)
	}
}

type fakeSearcher struct {
	candidates []acquisition.Candidate
}

func (s *fakeSearcher) Search(context.Context, string, int) ([]acquisition.Candidate, error) {
	return s.candidates, nil
}

func TestProvisionerSendsLowConfidenceToReview(t *testing.T) {
	core := newFakeCore()
	core.add("req-1", "track-1", "Artist Song")
	provider := &fakeProvider{dir: t.TempDir(), content: "audio"}
	runner, _, client := newRunner(t, core, provider, time.Second)
	runner.Searcher = &fakeSearcher{candidates: []acquisition.Candidate{
		{ID: "weak", Kind: "audio", Title: "Wrong Song", Confidence: 0.1, URL: "https://example.test/weak"},
	}}
	runner.Review = client

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("provider calls = %d, want none", len(provider.calls))
	}
	if len(core.reviews) != 1 {
		t.Fatalf("reviews = %#v, want one", core.reviews)
	}
	review := core.reviews[0]
	if review["canonical_id"] != "track-1" || review["service_id"] != "weak" || review["provider"] != "yt-dlp" {
		t.Fatalf("review = %#v, want the low-confidence candidate", review)
	}
	if len(core.completed) != 1 {
		t.Fatalf("completed = %#v, want req-1 after review", core.completed)
	}
}
