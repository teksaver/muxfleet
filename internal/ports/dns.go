package ports

import (
	"context"
)

// DNSRecord represents a DNS entry (AD-16).
type DNSRecord struct {
	Name  string `json:"name"`  // e.g. "vpn", "*"
	Type  string `json:"type"`  // "A", "AAAA", "CNAME"
	Value string `json:"value"` // IP or target
	TTL   int    `json:"ttl"`   // minimum allowed by provider, <= 60s for vpn
}

// DNS defines the DNS management port (Infomaniak, deSEC, Cloudflare) (AD-16).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type DNS interface {
	Name() string
	EnsureRecords(ctx context.Context, domain string, records []DNSRecord) error
	VerifyResolution(ctx context.Context, fqdn string, expectedIP string) (bool, error)
	DeleteRecords(ctx context.Context, domain string, records []DNSRecord) error
}
