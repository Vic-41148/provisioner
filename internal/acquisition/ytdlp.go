// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// YtDlp shells out to the external yt-dlp executable. No extraction logic
// lives in this process; yt-dlp is the only implementation.
type YtDlp struct {
	// Binary is the yt-dlp executable. Empty means resolve via PATH.
	Binary string
	// Runtime is the JS runtime (Deno by default) used by yt-dlp.
	Runtime string
	// Cookies is an optional cookie file. Empty means no cookies.
	Cookies string
	// POProvider is an optional PO-token provider executable.
	POProvider string
}

func (y *YtDlp) Name() string { return "yt-dlp" }

// runtimePath strips an optional name: prefix from a JS runtime spec so the
// health check stats the executable rather than the whole spec.
func runtimePath(spec string) string {
	spec = strings.TrimSpace(spec)
	if index := strings.IndexByte(spec, ':'); index > 0 {
		return spec[index+1:]
	}
	return spec
}

// searchTarget turns MusicBrainz metadata into a yt-dlp search. An explicit
// search prefix or URL is passed through unchanged so a caller keeps control.
func searchTarget(query string) string {
	query = strings.TrimSpace(query)
	lower := strings.ToLower(query)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "ytsearch") {
		return query
	}
	return "ytsearch1:" + query
}

func (y *YtDlp) binary() (string, error) {
	if strings.TrimSpace(y.Binary) != "" {
		if _, err := os.Stat(y.Binary); err != nil {
			return "", fmt.Errorf("yt-dlp binary not found: %w", err)
		}
		return y.Binary, nil
	}
	resolved, err := exec.LookPath("yt-dlp")
	if err != nil {
		return "", fmt.Errorf("yt-dlp not on PATH: %w", err)
	}
	return resolved, nil
}

func (y *YtDlp) Health(ctx context.Context) (Health, error) {
	bin, err := y.binary()
	if err != nil {
		return Health{Available: false, Reason: err.Error()}, nil
	}
	if strings.TrimSpace(y.Runtime) != "" {
		if _, err := os.Stat(runtimePath(y.Runtime)); err != nil {
			return Health{Available: false, Reason: "js runtime unavailable: " + err.Error()}, nil
		}
	}
	if strings.TrimSpace(y.Cookies) != "" {
		if _, err := os.Stat(y.Cookies); err != nil {
			return Health{Available: false, Reason: "cookie file unavailable: " + err.Error()}, nil
		}
	}
	if strings.TrimSpace(y.POProvider) != "" {
		if _, err := os.Stat(y.POProvider); err != nil {
			return Health{Available: false, Reason: "po-token provider unavailable: " + err.Error()}, nil
		}
	}
	// Confirm the binary actually runs.
	if out, err := exec.CommandContext(ctx, bin, "--version").Output(); err != nil {
		return Health{Available: false, Reason: "yt-dlp failed to start: " + err.Error()}, nil
	} else if strings.TrimSpace(string(out)) == "" {
		return Health{Available: false, Reason: "yt-dlp returned no version"}, nil
	}
	return Health{Available: true}, nil
}

func (y *YtDlp) Acquire(ctx context.Context, request Request) (string, error) {
	if strings.TrimSpace(request.Query) == "" {
		return "", fmt.Errorf("yt-dlp: empty query")
	}
	bin, err := y.binary()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "provisioner-*")
	if err != nil {
		return "", fmt.Errorf("yt-dlp: create work dir: %w", err)
	}
	defer os.RemoveAll(dir)

	args := []string{
		"--no-playlist",
		"--extract-audio",
		"--audio-format", "opus",
		"--output", filepath.Join(dir, "%(title)s.%(ext)s"),
	}
	if runtime := strings.TrimSpace(y.Runtime); runtime != "" {
		// A bare path selects Deno, matching the documented default; an
		// explicit name:path lets a host use another supported runtime.
		if !strings.Contains(runtime, ":") {
			runtime = "deno:" + runtime
		}
		args = append(args, "--js-runtimes", runtime)
	}
	if strings.TrimSpace(y.Cookies) != "" {
		args = append(args, "--cookies", y.Cookies)
	}
	args = append(args, searchTarget(request.Query))

	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("yt-dlp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Find the downloaded file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("yt-dlp: read work dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		final, err := os.CreateTemp("", "provisioner-*"+filepath.Ext(entry.Name()))
		if err != nil {
			return "", fmt.Errorf("yt-dlp: create output: %w", err)
		}
		finalPath := final.Name()
		if err := final.Close(); err != nil {
			os.Remove(finalPath)
			return "", fmt.Errorf("yt-dlp: close output: %w", err)
		}
		if err := os.Rename(filepath.Join(dir, entry.Name()), finalPath); err != nil {
			os.Remove(finalPath)
			return "", fmt.Errorf("yt-dlp: move output: %w", err)
		}
		return finalPath, nil
	}
	return "", fmt.Errorf("yt-dlp: no output file produced")
}
