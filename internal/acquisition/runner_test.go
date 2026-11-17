// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeQueue struct {
	jobs         []Job
	claimErr     error
	claimed      []string
	completed    []string
	failed       []string
	failMessages []string
}

func (q *fakeQueue) Queued(context.Context) ([]Job, error) { return q.jobs, nil }

func (q *fakeQueue) Claim(_ context.Context, id string) error {
	if q.claimErr != nil {
		return q.claimErr
	}
	q.claimed = append(q.claimed, id)
	return nil
}

func (q *fakeQueue) Complete(_ context.Context, id string) error {
	q.completed = append(q.completed, id)
	return nil
}

func (q *fakeQueue) Fail(_ context.Context, id, message string) error {
	q.failed = append(q.failed, id)
	q.failMessages = append(q.failMessages, message)
	return nil
}

type fakeProvider struct {
	path  string
	err   error
	calls []Request
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) Acquire(_ context.Context, request Request) (string, error) {
	p.calls = append(p.calls, request)
	return p.path, p.err
}
func (p *fakeProvider) Health(context.Context) (Health, error) {
	return Health{Available: true}, nil
}

type fakeIngest struct {
	calls int
	job   Job
	path  string
	hash  string
	name  string
	err   error
}

func (i *fakeIngest) Ingest(_ context.Context, job Job, path, hash, name string) error {
	i.calls++
	i.job = job
	i.path = path
	i.hash = hash
	i.name = name
	return i.err
}

func TestRunnerCompletesRequest(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "song.opus")
	content := []byte("downloaded audio")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}
	sum := sha256.Sum256(content)
	queue := &fakeQueue{jobs: []Job{{ID: "req-1", CanonicalID: "track-1", Query: "Artist Song"}}}
	provider := &fakeProvider{path: path}
	ingest := &fakeIngest{}

	runner := Runner{Queue: queue, Provider: provider, Ingest: ingest, Between: time.Millisecond, Sleep: func(time.Duration) {}}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(queue.claimed) != 1 || queue.claimed[0] != "req-1" {
		t.Fatalf("claimed = %v, want req-1", queue.claimed)
	}
	if len(queue.completed) != 1 || queue.completed[0] != "req-1" {
		t.Fatalf("completed = %v, want req-1", queue.completed)
	}
	if len(queue.failed) != 0 {
		t.Fatalf("failed = %v, want none", queue.failed)
	}
	if len(provider.calls) != 1 || provider.calls[0].CanonicalID != "track-1" {
		t.Fatalf("provider calls = %#v, want one for track-1", provider.calls)
	}
	if ingest.calls != 1 {
		t.Fatalf("ingest calls = %d, want 1", ingest.calls)
	}
	if ingest.hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("ingest hash = %q, want the download digest", ingest.hash)
	}
	if ingest.name != "song.opus" {
		t.Fatalf("ingest name = %q, want the download file name", ingest.name)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("download still exists after ingest: %v", err)
	}
}

func TestRunnerRecordsProviderFailure(t *testing.T) {
	t.Parallel()

	queue := &fakeQueue{jobs: []Job{{ID: "req-1", CanonicalID: "track-1", Query: "Artist Song"}}}
	provider := &fakeProvider{err: errors.New("provider exited 1")}
	ingest := &fakeIngest{}

	runner := Runner{Queue: queue, Provider: provider, Ingest: ingest, Between: time.Millisecond, Sleep: func(time.Duration) {}}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(queue.completed) != 0 {
		t.Fatalf("completed = %v, want none", queue.completed)
	}
	if len(queue.failed) != 1 || queue.failed[0] != "req-1" {
		t.Fatalf("failed = %v, want req-1", queue.failed)
	}
	if queue.failMessages[0] != "provider exited 1" {
		t.Fatalf("failure message = %q, want the provider error", queue.failMessages[0])
	}
	if ingest.calls != 0 {
		t.Fatalf("ingest calls = %d, want none", ingest.calls)
	}
}

func TestRunnerRecordsIngestFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "song.opus")
	if err := os.WriteFile(path, []byte("downloaded audio"), 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}
	queue := &fakeQueue{jobs: []Job{{ID: "req-1", CanonicalID: "track-1", Query: "Artist Song"}}}
	provider := &fakeProvider{path: path}
	ingest := &fakeIngest{err: errors.New("upload rejected")}

	runner := Runner{Queue: queue, Provider: provider, Ingest: ingest, Between: time.Millisecond, Sleep: func(time.Duration) {}}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(queue.completed) != 0 {
		t.Fatalf("completed = %v, want none", queue.completed)
	}
	if len(queue.failed) != 1 || queue.failMessages[0] != "upload rejected" {
		t.Fatalf("failed = %v %v, want the ingest error", queue.failed, queue.failMessages)
	}
}

func TestRunnerStopsWhenClaimRefused(t *testing.T) {
	t.Parallel()

	queue := &fakeQueue{jobs: []Job{{ID: "req-1"}}, claimErr: ErrStopped}
	provider := &fakeProvider{}

	runner := Runner{Queue: queue, Provider: provider, Ingest: &fakeIngest{}, Between: time.Millisecond, Sleep: func(time.Duration) {}}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("provider calls = %d, want none", len(provider.calls))
	}
}

func TestRunnerSpacesDownloads(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "song.opus")
	if err := os.WriteFile(path, []byte("downloaded audio"), 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}
	queue := &fakeQueue{jobs: []Job{{ID: "req-1", CanonicalID: "track-1"}, {ID: "req-2", CanonicalID: "track-2"}}}
	var slept []time.Duration

	runner := Runner{
		Queue:    queue,
		Provider: &fakeProvider{path: path},
		Ingest:   &fakeIngest{},
		Between:  3 * time.Second,
		Sleep:    func(d time.Duration) { slept = append(slept, d) },
	}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(slept) != 1 || slept[0] != 3*time.Second {
		t.Fatalf("slept = %v, want one three second pause", slept)
	}
}

type fakeSearcher struct {
	candidates []Candidate
	err        error
	queries    []string
}

func (s *fakeSearcher) Search(_ context.Context, query string, _ int) ([]Candidate, error) {
	s.queries = append(s.queries, query)
	return s.candidates, s.err
}

type fakeReview struct {
	candidates []Candidate
	reasons    []string
	err        error
}

func (r *fakeReview) AddReview(_ context.Context, _ Job, candidate Candidate, reason string) error {
	r.candidates = append(r.candidates, candidate)
	r.reasons = append(r.reasons, reason)
	return r.err
}

func TestRunnerDownloadsBestCandidate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "song.opus")
	if err := os.WriteFile(path, []byte("downloaded audio"), 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}
	queue := &fakeQueue{jobs: []Job{{ID: "req-1", CanonicalID: "track-1", Query: "Artist Song"}}}
	provider := &fakeProvider{path: path}
	searcher := &fakeSearcher{candidates: []Candidate{
		{ID: "video", Kind: "video", Confidence: 0.99, URL: "https://example.test/video"},
		{ID: "audio", Kind: "audio", Confidence: 0.8, URL: "https://example.test/audio"},
	}}
	ingest := &fakeIngest{}

	runner := Runner{
		Queue: queue, Provider: provider, Ingest: ingest, Searcher: searcher,
		Between: time.Millisecond, Sleep: func(time.Duration) {},
	}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("provider calls = %d, want one", len(provider.calls))
	}
	if provider.calls[0].Source != "https://example.test/audio" {
		t.Fatalf("source = %q, want the audio candidate", provider.calls[0].Source)
	}
	if len(queue.completed) != 1 {
		t.Fatalf("completed = %v, want req-1", queue.completed)
	}
}

func TestRunnerSendsLowConfidenceToReview(t *testing.T) {
	t.Parallel()

	queue := &fakeQueue{jobs: []Job{{ID: "req-1", CanonicalID: "track-1", Query: "Artist Song"}}}
	provider := &fakeProvider{}
	searcher := &fakeSearcher{candidates: []Candidate{
		{ID: "weak", Kind: "audio", Confidence: 0.1, Title: "Wrong Song", URL: "https://example.test/weak"},
	}}
	review := &fakeReview{}

	runner := Runner{
		Queue: queue, Provider: provider, Ingest: &fakeIngest{}, Searcher: searcher, Review: review,
		Between: time.Millisecond, Sleep: func(time.Duration) {},
	}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("provider calls = %d, want none for a low-confidence match", len(provider.calls))
	}
	if len(review.candidates) != 1 || review.candidates[0].ID != "weak" {
		t.Fatalf("review candidates = %#v, want weak", review.candidates)
	}
	if len(queue.completed) != 1 {
		t.Fatalf("completed = %v, want req-1 after review", queue.completed)
	}
}

func TestRunnerFailsLowConfidenceWithoutReview(t *testing.T) {
	t.Parallel()

	queue := &fakeQueue{jobs: []Job{{ID: "req-1", CanonicalID: "track-1", Query: "Artist Song"}}}
	provider := &fakeProvider{}
	searcher := &fakeSearcher{candidates: []Candidate{
		{ID: "weak", Kind: "audio", Confidence: 0.1, Title: "Wrong Song"},
	}}

	runner := Runner{
		Queue: queue, Provider: provider, Ingest: &fakeIngest{}, Searcher: searcher,
		Between: time.Millisecond, Sleep: func(time.Duration) {},
	}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("provider calls = %d, want none", len(provider.calls))
	}
	if len(queue.failed) != 1 {
		t.Fatalf("failed = %v, want req-1", queue.failed)
	}
	if len(queue.completed) != 0 {
		t.Fatalf("completed = %v, want none", queue.completed)
	}
}

func TestRunnerUsesAnExactSourceWithoutSearching(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "song.opus")
	if err := os.WriteFile(path, []byte("downloaded audio"), 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}
	queue := &fakeQueue{jobs: []Job{{
		ID: "req-1", CanonicalID: "track-1", Query: "Artist Song",
		Source: "https://music.youtube.com/watch?v=v1",
	}}}
	provider := &fakeProvider{path: path}
	searcher := &fakeSearcher{}

	runner := Runner{
		Queue: queue, Provider: provider, Ingest: &fakeIngest{}, Searcher: searcher,
		Between: time.Millisecond, Sleep: func(time.Duration) {},
	}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(searcher.queries) != 0 {
		t.Fatalf("search queries = %v, want none with an exact source", searcher.queries)
	}
	if len(provider.calls) != 1 || provider.calls[0].Source != "https://music.youtube.com/watch?v=v1" {
		t.Fatalf("provider calls = %#v", provider.calls)
	}
}
