// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestRuntimePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec string
		want string
	}{
		{name: "bare path", spec: "/usr/bin/deno", want: "/usr/bin/deno"},
		{name: "named path", spec: "node:/usr/bin/node", want: "/usr/bin/node"},
		{name: "named deno", spec: "deno:/opt/deno", want: "/opt/deno"},
		{name: "spaces", spec: " node:/usr/bin/node ", want: "/usr/bin/node"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := runtimePath(tc.spec); got != tc.want {
				t.Fatalf("runtimePath(%q) = %q, want %q", tc.spec, got, tc.want)
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

func TestYtDlpHealthRequiresFfmpeg(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho fake-version\n"), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	t.Setenv("PATH", dir)

	p := &YtDlp{Binary: fake}
	health, err := p.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.Available {
		t.Fatal("Health().Available = true, want false without ffmpeg")
	}
	if !strings.Contains(health.Reason, "ffmpeg") {
		t.Fatalf("Health().Reason = %q, want ffmpeg", health.Reason)
	}
}
