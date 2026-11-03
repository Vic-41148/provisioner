// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

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
	"testing"
)

func TestClientQueueLifecycle(t *testing.T) {
	t.Parallel()

	var calls []string
	checkAuth := func(t *testing.T, r *http.Request) {
		t.Helper()
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization = %q, want the bearer token", got)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sync/v1/acquisition", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		if r.URL.Query().Get("state") != "queued" {
			t.Fatalf("state = %q, want queued", r.URL.Query().Get("state"))
		}
		calls = append(calls, "queued")
		_, _ = io.WriteString(w, `{"requests":[{"id":"req-1","canonical_id":"track-1","query":"Artist Song","state":"queued"}]}`)
	})
	mux.HandleFunc("POST /sync/v1/acquisition/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		calls = append(calls, "claim:"+r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /sync/v1/acquisition/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		calls = append(calls, "complete:"+r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /sync/v1/acquisition/{id}/fail", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		var payload struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode fail body: %v", err)
		}
		calls = append(calls, "fail:"+r.PathValue("id")+":"+payload.Error)
		_, _ = io.WriteString(w, `{"state":"queued"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	client := NewClient(server.URL, "secret")
	jobs, err := client.Queued(ctx)
	if err != nil {
		t.Fatalf("Queued() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "req-1" || jobs[0].CanonicalID != "track-1" {
		t.Fatalf("jobs = %#v, want req-1 for track-1", jobs)
	}
	if err := client.Claim(ctx, "req-1"); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := client.Complete(ctx, "req-1"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := client.Fail(ctx, "req-1", "provider exited 1"); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}

	want := []string{"queued", "claim:req-1", "complete:req-1", "fail:req-1:provider exited 1"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestClientClaimMapsDisabledToStopped(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"acquisition disabled"}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	if err := client.Claim(context.Background(), "req-1"); !errors.Is(err, ErrStopped) {
		t.Fatalf("Claim() error = %v, want ErrStopped", err)
	}
}

func TestClientIngestUploadsCachedRendition(t *testing.T) {
	t.Parallel()

	content := []byte("downloaded audio")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	path := filepath.Join(t.TempDir(), "song.opus")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}

	var got struct {
		hash, name, track, kind, contentRange, auth string
		body                                        []byte
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /sync/v1/library/uploads/{hash}", func(w http.ResponseWriter, r *http.Request) {
		got.hash = r.PathValue("hash")
		got.name = r.URL.Query().Get("name")
		got.track = r.URL.Query().Get("track")
		got.kind = r.URL.Query().Get("kind")
		got.contentRange = r.Header.Get("Content-Range")
		got.auth = r.Header.Get("Authorization")
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	})

	uploadServer := httptest.NewServer(mux)
	defer uploadServer.Close()
	client := NewClient(uploadServer.URL, "secret")
	job := Job{ID: "req-1", CanonicalID: "track-1"}
	if err := client.Ingest(context.Background(), job, path, hash, "song.opus"); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	if got.hash != hash || got.name != "song.opus" || got.track != "track-1" || got.kind != "cached" {
		t.Fatalf("upload fields = %#v, want the cached track upload", got)
	}
	if got.auth != "Bearer secret" {
		t.Fatalf("authorization = %q, want the bearer token", got.auth)
	}
	if got.contentRange != "bytes 0-15/16" {
		t.Fatalf("content range = %q, want the whole file", got.contentRange)
	}
	if string(got.body) != string(content) {
		t.Fatalf("body = %q, want the file content", got.body)
	}
}
