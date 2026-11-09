package ports

import (
	"context"

	"muxfleet/internal/domain"
)

// Forge defines the git forge interaction port (GitHub, GitLab, Forgejo) (AD-20).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type Forge interface {
	Name() string
	VerifyDeployKey(ctx context.Context, repoURL string, privateKey []byte) (domain.Proof, error)
}
