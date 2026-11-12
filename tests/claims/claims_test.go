// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

// Package claims keeps the provisioner's README and code in step. It mirrors
// the core repository's evidence check on a much smaller surface.
package claims

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type manifest struct {
	Version int     `json:"version"`
	Claims  []claim `json:"claims"`
}

type claim struct {
	ID       string   `json:"id"`
	Claim    string   `json:"claim"`
	Evidence []string `json:"evidence"`
}

func TestClaimsMatchReality(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	if m.Version != 1 {
		t.Fatalf("manifest version = %d, want 1", m.Version)
	}
	if len(m.Claims) == 0 {
		t.Fatal("manifest has no claims")
	}
	seen := make(map[string]bool, len(m.Claims))
	for _, c := range m.Claims {
		if c.ID == "" || seen[c.ID] {
			t.Fatalf("claim id %q is empty or duplicated", c.ID)
		}
		seen[c.ID] = true
		if strings.TrimSpace(c.Claim) == "" {
			t.Fatalf("%s: empty claim text", c.ID)
		}
		if len(c.Evidence) == 0 {
			t.Fatalf("%s: no evidence", c.ID)
		}
	}
	for _, c := range m.Claims {
		c := c
		t.Run(c.ID, func(t *testing.T) {
			for _, ev := range c.Evidence {
				if err := checkEvidence(root, ev); err != nil {
					t.Errorf("evidence %q: %v", ev, err)
				}
			}
		})
	}
}

func loadManifest(t *testing.T, root string) manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "tests", "claims", "claims.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
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

func checkEvidence(root, ev string) error {
	switch {
	case strings.HasPrefix(ev, "contains:"):
		path, token, err := splitToken(ev, "contains:")
		if err != nil {
			return err
		}
		return requireToken(root, path, token, true)
	case strings.HasPrefix(ev, "lacks:"):
		path, token, err := splitToken(ev, "lacks:")
		if err != nil {
			return err
		}
		return requireToken(root, path, token, false)
	case strings.HasPrefix(ev, "absent:"):
		rel := strings.TrimPrefix(ev, "absent:")
		if rel == "" {
			return fmt.Errorf("empty path")
		}
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return fmt.Errorf("%s exists", rel)
		} else if !os.IsNotExist(err) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown evidence prefix")
	}
}

func splitToken(ev, prefix string) (string, string, error) {
	rest := strings.TrimPrefix(ev, prefix)
	path, token, ok := strings.Cut(rest, "::")
	if !ok || path == "" || token == "" {
		return "", "", fmt.Errorf("want <path>::<token>")
	}
	return path, token, nil
}

func requireToken(root, rel, token string, want bool) error {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return err
	}
	has := strings.Contains(string(data), token)
	if want && !has {
		return fmt.Errorf("%s does not contain %q", rel, token)
	}
	if !want && has {
		return fmt.Errorf("%s unexpectedly contains %q", rel, token)
	}
	return nil
}
