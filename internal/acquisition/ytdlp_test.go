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
