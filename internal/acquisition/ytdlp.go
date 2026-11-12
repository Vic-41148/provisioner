// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"encoding/json"
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
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return Health{Available: false, Reason: "ffmpeg not on PATH: " + err.Error()}, nil
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
	args = y.appendRuntime(args)
	args = y.appendCookies(args)
	target := strings.TrimSpace(request.Source)
	if target == "" {
		target = searchTarget(request.Query)
	}
	args = append(args, target)

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

// Search lists candidates without downloading. The query keeps the same
// MusicBrainz metadata shape as Acquire.
func (y *YtDlp) Search(ctx context.Context, query string, limit int) ([]Candidate, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("yt-dlp: empty query")
	}
	bin, err := y.binary()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	args := []string{"--dump-single-json", "--flat-playlist", "--no-playlist"}
	args = y.appendRuntime(args)
	args = y.appendCookies(args)
	if strings.HasPrefix(strings.ToLower(query), "http://") || strings.HasPrefix(strings.ToLower(query), "https://") {
		args = append(args, query)
	} else {
		args = append(args, fmt.Sprintf("ytsearch%d:%s", limit, query))
	}
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp search: %w", err)
	}
	return parseSearchJSON(out, query)
}

func (y *YtDlp) appendRuntime(args []string) []string {
	if runtime := strings.TrimSpace(y.Runtime); runtime != "" {
		// A bare path selects Deno, matching the documented default; an
		// explicit name:path lets a host use another supported runtime.
		if !strings.Contains(runtime, ":") {
			runtime = "deno:" + runtime
		}
		args = append(args, "--js-runtimes", runtime)
	}
	return args
}

func (y *YtDlp) appendCookies(args []string) []string {
	if strings.TrimSpace(y.Cookies) != "" {
		args = append(args, "--cookies", y.Cookies)
	}
	return args
}

type searchEntry struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Uploader  string `json:"uploader"`
	Channel   string `json:"channel"`
	Extractor string `json:"extractor_key"`
	IEKey     string `json:"ie_key"`
	Duration  int    `json:"duration"`
}

func parseSearchJSON(data []byte, query string) ([]Candidate, error) {
	var payload struct {
		Entries []searchEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("yt-dlp search: decode: %w", err)
	}
	candidates := make([]Candidate, 0, len(payload.Entries))
	for _, entry := range payload.Entries {
		if entry.ID == "" && entry.URL == "" {
			continue
		}
		artist := strings.TrimSpace(entry.Uploader)
		if artist == "" {
			artist = strings.TrimSpace(entry.Channel)
		}
		url := entry.URL
		if url == "" && entry.ID != "" {
			url = "https://www.youtube.com/watch?v=" + entry.ID
		}
		candidates = append(candidates, Candidate{
			ID:         entry.ID,
			Title:      entry.Title,
			Artist:     artist,
			Kind:       classifyCandidate(entry.Title, artist),
			URL:        url,
			Confidence: Confidence(query, entry.Title, artist),
		})
	}
	return candidates, nil
}

func classifyCandidate(title, artist string) string {
	lowerTitle := strings.ToLower(title)
	lowerArtist := strings.ToLower(artist)
	for _, marker := range []string{"official music video", "official video", "music video"} {
		if strings.Contains(lowerTitle, marker) {
			return "video"
		}
	}
	for _, marker := range []string{"official audio", "lyrics", "lyric video", "audio"} {
		if strings.Contains(lowerTitle, marker) {
			return "audio"
		}
	}
	if strings.Contains(lowerArtist, "topic") {
		return "audio"
	}
	if lowerTitle != "" {
		return "video"
	}
	return "upload"
}
