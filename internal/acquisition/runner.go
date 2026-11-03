// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Job is the queue-facing shape of one acquisition request.
type Job struct {
	ID          string `json:"id"`
	CanonicalID string `json:"canonical_id"`
	Query       string `json:"query"`
	State       string `json:"state,omitempty"`
	Attempts    int    `json:"attempts,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// Queue is the core acquisition_request API the runner drains.
type Queue interface {
	Queued(context.Context) ([]Job, error)
	Claim(context.Context, string) error
	Complete(context.Context, string) error
	Fail(context.Context, string, string) error
}

// Ingest stores a downloaded file through the core's normal upload path.
type Ingest interface {
	Ingest(context.Context, Job, string, string, string) error
}

// ErrStopped reports a queue that refused a claim because the worker was
// switched off. A runner stops for the pass instead of failing the request.
var ErrStopped = errors.New("acquisition: worker stopped")

// Runner drains queued requests one at a time. Provider failures are recorded
// on the request and do not stop the pass.
type Runner struct {
	Queue    Queue
	Provider Provider
	Ingest   Ingest
	// Between is the delay between two downloads. A zero value uses five
	// seconds.
	Between time.Duration
	// Timeout bounds one download and ingest. A zero value leaves the caller's
	// context in charge.
	Timeout time.Duration
	// Sleep is injectable for tests. A nil value uses time.Sleep.
	Sleep func(time.Duration)
	// Logf records per-request outcomes. A nil value discards them.
	Logf func(string, ...any)
}

// RunOnce claims and processes every queued request once.
func (r Runner) RunOnce(ctx context.Context) error {
	if r.Queue == nil {
		return errors.New("acquisition: queue is required")
	}
	if r.Provider == nil {
		return errors.New("acquisition: provider is required")
	}
	if r.Ingest == nil {
		return errors.New("acquisition: ingest is required")
	}
	between := r.Between
	if between <= 0 {
		between = 5 * time.Second
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	logf := r.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	jobs, err := r.Queue.Queued(ctx)
	if err != nil {
		return err
	}
	for i, job := range jobs {
		if i > 0 && between > 0 {
			sleep(between)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.Queue.Claim(ctx, job.ID); err != nil {
			if errors.Is(err, ErrStopped) {
				return nil
			}
			logf("claim skipped", "id", job.ID, "error", err)
			continue
		}
		if err := r.process(ctx, job); err != nil {
			logf("acquisition failed", "id", job.ID, "error", err)
		}
	}
	return nil
}

func (r Runner) process(ctx context.Context, job Job) error {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	path, err := r.Provider.Acquire(ctx, Request{
		ID:          job.ID,
		CanonicalID: job.CanonicalID,
		Query:       job.Query,
	})
	if err != nil {
		return r.fail(ctx, job, err)
	}
	defer os.Remove(path)

	hash, err := fileHash(path)
	if err != nil {
		return r.fail(ctx, job, err)
	}
	if err := r.Ingest.Ingest(ctx, job, path, hash, filepath.Base(path)); err != nil {
		return r.fail(ctx, job, err)
	}
	if err := r.Queue.Complete(ctx, job.ID); err != nil {
		return err
	}
	return nil
}

func (r Runner) fail(ctx context.Context, job Job, cause error) error {
	if err := r.Queue.Fail(ctx, job.ID, cause.Error()); err != nil {
		return fmt.Errorf("record failure for %s: %w", job.ID, err)
	}
	return cause
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("acquisition: open download: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("acquisition: hash download: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
