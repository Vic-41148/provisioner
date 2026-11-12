// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsDatacenter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		org  string
		want bool
	}{
		{name: "google", org: "AS15169 Google LLC", want: true},
		{name: "google cloud", org: "AS396982 Google Cloud", want: true},
		{name: "amazon", org: "AS16509 Amazon.com, Inc.", want: true},
		{name: "microsoft", org: "AS8075 Microsoft Corporation", want: true},
		{name: "digitalocean", org: "AS14061 DigitalOcean, LLC", want: true},
		{name: "hetzner", org: "AS24940 Hetzner Online GmbH", want: true},
		{name: "cloudflare", org: "AS13335 Cloudflare, Inc.", want: true},
		{name: "colocation by name", org: "AS64500 Some Colocation Provider", want: true},
		{name: "vps by name", org: "AS64503 Fast VPS Ltd", want: true},
		{name: "data center by name", org: "AS64504 Data Center Networks", want: true},
		{name: "cloud by name", org: "AS64505 Example Cloud Ltd", want: true},
		{name: "asn wins over residential marker", org: "AS15169 Google Fiber LLC", want: true},
		{name: "google fiber is residential", org: "AS16591 Google Fiber Inc.", want: false},
		{name: "comcast is residential", org: "AS7922 Comcast Cable Communications, LLC", want: false},
		{name: "telekom is residential", org: "AS3320 Deutsche Telekom AG", want: false},
		{name: "broadband is residential", org: "AS64501 Example Broadband Ltd", want: false},
		{name: "residential hosting is allowed", org: "AS64502 Residential Hosting Co", want: false},
		{name: "empty organisation", org: "", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, reason := IsDatacenter(Egress{IP: "203.0.113.10", Org: tc.org})
			if got != tc.want {
				t.Fatalf("IsDatacenter(%q) = %v (%s), want %v", tc.org, got, reason, tc.want)
			}
			if got && reason == "" {
				t.Fatalf("IsDatacenter(%q) returned no reason", tc.org)
			}
		})
	}
}

type fakeEgress struct {
	egress Egress
	err    error
}

func (f fakeEgress) Lookup(context.Context) (Egress, error) {
	return f.egress, f.err
}

func TestCheckEgress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lookup  EgressLookup
		wantErr string
	}{
		{
			name:    "datacenter refused",
			lookup:  fakeEgress{egress: Egress{IP: "203.0.113.10", Org: "AS16509 Amazon.com, Inc."}},
			wantErr: "datacenter egress",
		},
		{
			name:   "home connection allowed",
			lookup: fakeEgress{egress: Egress{IP: "203.0.113.10", Org: "AS64501 Example Broadband Ltd"}},
		},
		{
			name:    "lookup failure is fatal",
			lookup:  fakeEgress{err: errors.New("no route")},
			wantErr: "egress lookup failed",
		},
		{
			name:    "missing lookup is fatal",
			lookup:  nil,
			wantErr: "not configured",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckEgress(context.Background(), tc.lookup)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckEgress() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CheckEgress() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestIPInfoLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		want    Egress
		wantErr string
	}{
		{
			name:   "valid response",
			status: http.StatusOK,
			body:   `{"ip":"203.0.113.10","org":"AS64501 Example Broadband Ltd"}`,
			want:   Egress{IP: "203.0.113.10", Org: "AS64501 Example Broadband Ltd"},
		},
		{
			name:    "server error",
			status:  http.StatusServiceUnavailable,
			body:    `unavailable`,
			wantErr: "503",
		},
		{
			name:    "invalid json",
			status:  http.StatusOK,
			body:    `{`,
			wantErr: "decode",
		},
		{
			name:    "missing organisation",
			status:  http.StatusOK,
			body:    `{"ip":"203.0.113.10"}`,
			wantErr: "no ip or organisation",
		},
		{
			name:    "missing address",
			status:  http.StatusOK,
			body:    `{"org":"AS64501 Example Broadband Ltd"}`,
			wantErr: "no ip or organisation",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Accept"); got != "application/json" {
					t.Errorf("Accept = %q", got)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			got, err := IPInfoLookup{URL: server.URL, Client: server.Client()}.Lookup(context.Background())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Lookup() = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lookup() = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Lookup() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
