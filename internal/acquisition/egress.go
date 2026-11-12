// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Egress is the public address and organisation the lookup reported.
type Egress struct {
	IP  string
	Org string
}

// EgressLookup resolves the host's public egress. It is an interface so the
// refusal can be tested without a network.
type EgressLookup interface {
	Lookup(ctx context.Context) (Egress, error)
}

// IPInfoLookup asks ipinfo.io for the public address and ASN organisation.
// The URL is configurable so a host can use its own endpoint.
type IPInfoLookup struct {
	URL    string
	Client *http.Client
}

func (l IPInfoLookup) Lookup(ctx context.Context) (Egress, error) {
	target := strings.TrimSpace(l.URL)
	if target == "" {
		target = "https://ipinfo.io/json"
	}
	client := l.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Egress{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "groovesync-provisioner")

	resp, err := client.Do(req)
	if err != nil {
		return Egress{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Egress{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		IP  string `json:"ip"`
		Org string `json:"org"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Egress{}, fmt.Errorf("decode egress response: %w", err)
	}
	payload.IP = strings.TrimSpace(payload.IP)
	payload.Org = strings.TrimSpace(payload.Org)
	if payload.IP == "" || payload.Org == "" {
		return Egress{}, fmt.Errorf("egress response has no ip or organisation")
	}
	return Egress{IP: payload.IP, Org: payload.Org}, nil
}

// CheckEgress refuses to run when the public address belongs to a known
// hosting provider. A failed lookup is also fatal: an unknown egress cannot be
// shown to be a home connection.
func CheckEgress(ctx context.Context, lookup EgressLookup) error {
	if lookup == nil {
		return fmt.Errorf("egress lookup is not configured")
	}
	egress, err := lookup.Lookup(ctx)
	if err != nil {
		return fmt.Errorf("egress lookup failed: %w", err)
	}
	if datacenter, reason := IsDatacenter(egress); datacenter {
		return fmt.Errorf("refusing to run on a datacenter egress %s (%s): %s", egress.IP, egress.Org, reason)
	}
	return nil
}

var asnPattern = regexp.MustCompile(`\bas(\d+)\b`)

// IsDatacenter reports whether the organisation names a hosting provider. The
// ASN list is the precise signal; the name markers catch providers whose ASN is
// not listed yet. Residential markers keep an ISP that resells a listed brand
// from being refused.
func IsDatacenter(egress Egress) (bool, string) {
	org := strings.ToLower(strings.TrimSpace(egress.Org))
	if org == "" {
		return false, ""
	}
	if match := asnPattern.FindStringSubmatch(org); match != nil {
		asn := "as" + match[1]
		if _, ok := datacenterASNs[asn]; ok {
			return true, "hosting ASN " + strings.ToUpper(asn)
		}
	}
	for _, marker := range datacenterMarkers {
		if !strings.Contains(org, marker) {
			continue
		}
		if hasResidentialMarker(org) {
			return false, ""
		}
		return true, "hosting name " + marker
	}
	return false, ""
}

func hasResidentialMarker(org string) bool {
	for _, marker := range residentialMarkers {
		if strings.Contains(org, marker) {
			return true
		}
	}
	return false
}

// datacenterASNs lists the large clouds and hosting providers. It is not
// exhaustive; the name markers cover the rest.
var datacenterASNs = map[string]struct{}{
	"as15169":  {}, // Google
	"as396982": {}, // Google Cloud
	"as16509":  {}, // Amazon
	"as14618":  {}, // Amazon
	"as8075":   {}, // Microsoft
	"as14061":  {}, // DigitalOcean
	"as63949":  {}, // Linode, now Akamai
	"as20940":  {}, // Akamai
	"as16625":  {}, // Akamai
	"as16276":  {}, // OVH
	"as24940":  {}, // Hetzner
	"as213230": {}, // Hetzner
	"as20473":  {}, // Vultr
	"as31898":  {}, // Oracle Cloud
	"as13335":  {}, // Cloudflare
	"as209242": {}, // Cloudflare
	"as9009":   {}, // M247
	"as51167":  {}, // Contabo
	"as12876":  {}, // Scaleway
	"as60781":  {}, // LeaseWeb
	"as28753":  {}, // LeaseWeb
	"as49505":  {}, // Selectel
	"as45102":  {}, // Alibaba Cloud
	"as37963":  {}, // Alibaba Cloud
	"as132203": {}, // Tencent Cloud
	"as45090":  {}, // Tencent Cloud
	"as36352":  {}, // ColoCrossing
	"as53667":  {}, // FranTech, BuyVM
	"as62240":  {}, // Clouvider
	"as49981":  {}, // WorldStream
	"as197540": {}, // netcup
	"as8560":   {}, // IONOS
	"as6724":   {}, // Strato
	"as34011":  {}, // Host Europe
	"as29802":  {}, // Hivelocity
	"as30633":  {}, // LeaseWeb USA
	"as40065":  {}, // CNServers
	"as6939":   {}, // Hurricane Electric
	"as174":    {}, // Cogent
	"as2914":   {}, // NTT
	"as3356":   {}, // Lumen
	"as1299":   {}, // Arelion
	"as3257":   {}, // GTT
	"as6461":   {}, // Zayo
	"as9002":   {}, // RETN
	"as199524": {}, // Gcore
	"as35913":  {}, // DediPath
	"as46844":  {}, // Sharktech
	"as22612":  {}, // Namecheap
	"as32244":  {}, // Liquid Web
	"as26347":  {}, // DreamHost
	"as26496":  {}, // GoDaddy
	"as46606":  {}, // Unified Layer, Bluehost
}

var datacenterMarkers = []string{
	"amazon",
	"aws",
	"google",
	"microsoft",
	"azure",
	"digitalocean",
	"linode",
	"akamai",
	"ovh",
	"hetzner",
	"vultr",
	"oracle cloud",
	"cloudflare",
	"leaseweb",
	"scaleway",
	"contabo",
	"upcloud",
	"hostinger",
	"equinix",
	"alibaba",
	"tencent",
	"colocation",
	"colo crossing",
	"datacenter",
	"data center",
	"hosting",
	"dedicated server",
	"vps",
	"cloud",
}

var residentialMarkers = []string{
	"fiber",
	"fibre",
	"broadband",
	"cable",
	"telecom",
	"communications",
	"wireless",
	"mobile",
	"cellular",
	"residential",
	"internet service",
	"satellite",
	"fixed line",
}
