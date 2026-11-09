package ports

import (
	"context"
	"time"

	"muxfleet/internal/domain"
)

// ProviderResource represents a cloud resource tagged for muxfleet (AD-7).
type ProviderResource struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"` // "server", "ip", "firewall", "volume", "backup"
	Name      string            `json:"name"`
	Region    string            `json:"region"`
	Zone      string            `json:"zone,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Tags      map[string]string `json:"tags"`
}

// ServerOffer represents a machine offering from a cloud provider (AD-6, R6).
type ServerOffer struct {
	ProviderName  string          `json:"provider_name"`
	MachineType   string          `json:"machine_type"`
	VCPUs         int             `json:"vcpus"`
	RAMGigabytes  int             `json:"ram_gb"`
	DiskGigabytes int             `json:"disk_gb"`
	Pricing       *domain.Pricing `json:"pricing"`
	Available     bool            `json:"available"`
	ProvenAt      time.Time       `json:"proven_at"`
}

// ServerCreateRequest contains required info to create a VPS (AD-6, AD-22).
type ServerCreateRequest struct {
	ServerName  string `json:"server_name"`
	MachineType string `json:"machine_type"`
	Region      string `json:"region"`
	// UserData carries the bootstrap private keys (AD-10): never serialized, so never in a
	// --json output, a journal or a notification.
	UserData          string            `json:"-"`
	Tags              map[string]string `json:"tags"`
	AdminSSHPublicKey string            `json:"admin_ssh_public_key"`
}

// ServerInfo contains operational details of a created server.
type ServerInfo struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	PublicIPv4 string            `json:"public_ipv4"`
	PublicIPv6 string            `json:"public_ipv6,omitempty"`
	Status     string            `json:"status"`
	Tags       map[string]string `json:"tags"`
}

// Provider defines the hexagonal port for cloud VPS providers (Scaleway, Hetzner) (AD-1, AD-6, AD-7).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type Provider interface {
	Name() string
	ValidateCredentials(ctx context.Context) error
	GetOffers(ctx context.Context) ([]ServerOffer, error)
	CreateServer(ctx context.Context, req ServerCreateRequest) (*ServerInfo, error)
	GetServer(ctx context.Context, serverID string) (*ServerInfo, error)
	Inventory(ctx context.Context, serverName string) ([]ProviderResource, error)
	DestroyServer(ctx context.Context, serverID string) error
	DestroyResource(ctx context.Context, res ProviderResource) error
	ProveZeroResources(ctx context.Context, serverName string) (domain.Proof, error)
}
