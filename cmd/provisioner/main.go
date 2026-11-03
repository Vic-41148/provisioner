// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

// Command provisioner drains GrooveSync acquisition requests. It talks to
// syncd over its HTTP interface and shells out to yt-dlp for the download.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Vic-41148/provisioner/internal/acquisition"
)

type config struct {
	Server     string
	Token      string
	TokenFile  string
	Once       bool
	Interval   time.Duration
	Between    time.Duration
	Timeout    time.Duration
	Binary     string
	JSRuntime  string
	Cookies    string
	POProvider string
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.Server, "server", envOr("SYNCD_URL", "http://127.0.0.1:8080"), "syncd base URL")
	flag.StringVar(&cfg.Token, "token", "", "admin token; prefer -token-file or SYNCD_TOKEN")
	flag.StringVar(&cfg.TokenFile, "token-file", "", "file holding the admin token; defaults to ~/.syncd-token")
	flag.BoolVar(&cfg.Once, "once", false, "process one pass and exit")
	flag.DurationVar(&cfg.Interval, "interval", 30*time.Second, "delay between passes")
	flag.DurationVar(&cfg.Between, "between", 5*time.Second, "delay between two downloads")
	flag.DurationVar(&cfg.Timeout, "timeout", 15*time.Minute, "timeout for one download and ingest")
	flag.StringVar(&cfg.Binary, "yt-dlp", "", "path to the yt-dlp executable; empty resolves via PATH")
	flag.StringVar(&cfg.JSRuntime, "js-runtime", "", "JS runtime as name:path or a Deno path; empty lets yt-dlp choose")
	flag.StringVar(&cfg.Cookies, "cookies", "", "optional cookie file")
	flag.StringVar(&cfg.POProvider, "po-provider", "", "optional PO-token provider executable")
	flag.Parse()

	token, err := loadToken(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if strings.TrimSpace(cfg.Server) == "" {
		log.Fatal("server URL is required")
	}

	provider := &acquisition.YtDlp{
		Binary:     cfg.Binary,
		Runtime:    cfg.JSRuntime,
		Cookies:    cfg.Cookies,
		POProvider: cfg.POProvider,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	health, err := provider.Health(ctx)
	if err != nil {
		log.Fatalf("provider health check failed: %v", err)
	}
	if !health.Available {
		log.Fatalf("provider unavailable: %s", health.Reason)
	}

	client := acquisition.NewClient(cfg.Server, token)
	runner := acquisition.Runner{
		Queue:    client,
		Provider: provider,
		Ingest:   client,
		Between:  cfg.Between,
		Timeout:  cfg.Timeout,
		Logf:     log.Printf,
	}

	if cfg.Once {
		if err := runner.RunOnce(ctx); err != nil {
			log.Fatalf("run once: %v", err)
		}
		return
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		if err := runner.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("run: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func loadToken(cfg config) (string, error) {
	if token := strings.TrimSpace(cfg.Token); token != "" {
		return token, nil
	}
	if token := strings.TrimSpace(os.Getenv("SYNCD_TOKEN")); token != "" {
		return token, nil
	}
	path := strings.TrimSpace(cfg.TokenFile)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, ".syncd-token")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("token file is empty")
	}
	return token, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
