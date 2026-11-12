// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

// Package acquisition is the plugin-side worker. It depends on the core only
// through the acquisition_request shape and the normal file ingest path.
package acquisition

import (
	"context"
	"fmt"
)

// Request mirrors the core acquisition_request. It carries MusicBrainz
// metadata in Query and never Spotify identifiers. Source, when set, is the
// chosen candidate the provider should download instead of searching again.
type Request struct {
	ID          string
	CanonicalID string
	Query       string
	Source      string
}

// Provider downloads one request and returns the path of the file it produced.
// The caller ingests that file through the normal library path.
type Provider interface {
	Name() string
	Acquire(ctx context.Context, request Request) (string, error)
	Health(ctx context.Context) (Health, error)
}

// Searcher lists candidates for a query without downloading anything.
type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]Candidate, error)
}

// Review records a match that needs a human decision instead of a download.
type Review interface {
	AddReview(ctx context.Context, job Job, candidate Candidate, reason string) error
}

// Health reports whether the provider can run right now.
type Health struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// ErrUnavailable reports a provider that failed its startup health check.
var ErrUnavailable = fmt.Errorf("acquisition: provider unavailable")
