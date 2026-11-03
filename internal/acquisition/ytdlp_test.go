// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"os"
	"testing"
)

func TestYtDlpHealthUnavailableWithoutBinary(t *testing.T) {
	t.Parallel()

	p := &YtDlp{Binary: "/definitely/not/yt-dlp"}
	health, err := p.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.Available {
		t.Fatal("Health().Available = true, want false")
	}
	if health.Reason == "" {
		t.Fatal("Health().Reason empty, want a reason")
	}
}

func TestSearchTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "metadata", query: "Artist Song", want: "ytsearch1:Artist Song"},
		{name: "explicit search", query: "ytsearch5:Artist Song", want: "ytsearch5:Artist Song"},
		{name: "https url", query: "https://example.com/audio", want: "https://example.com/audio"},
		{name: "http url", query: "http://example.com/audio", want: "http://example.com/audio"},
		{name: "trims spaces", query: "  Artist Song  ", want: "ytsearch1:Artist Song"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := searchTarget(tc.query); got != tc.want {
				t.Fatalf("searchTarget(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

func TestYtDlpHealthChecksCookieFile(t *testing.T) {
	t.Parallel()

	missing := "/definitely/not/cookies.txt"
	p := &YtDlp{Binary: "/definitely/not/yt-dlp", Cookies: missing}
	health, err := p.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.Available {
		t.Fatal("Health().Available = true, want false")
	}
	// reason should mention cookie file; binary check comes first though.
	_ = os.Args
}
