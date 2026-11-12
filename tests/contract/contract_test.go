// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

// Package contract pins the provisioner side of the core HTTP contract.
package contract

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vic-41148/provisioner/internal/acquisition"
)

func TestJobJSONShape(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(acquisition.Job{
		ID:          "req-1",
		CanonicalID: "track-1",
		Query:       "Artist Song",
		State:       "queued",
		Attempts:    1,
		LastError:   "boom",
	})
	if err != nil {
		t.Fatalf("marshal Job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal Job: %v", err)
	}
	for _, key := range []string{"id", "canonical_id", "query", "state", "attempts", "last_error"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("Job JSON is missing %q: %s", key, body)
		}
	}
}

func TestPluginNeverOpensCoreDatabase(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	if strings.Contains(string(out), "groovesync/server/internal") {
		t.Fatal("provisioner imports a core internal package")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
