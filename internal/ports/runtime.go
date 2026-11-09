package ports

import (
	"context"
)

// Runtime defines the language runtime and pinned APT repository port (AD-18, AD-28).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type Runtime interface {
	Name() string
	EnsureRepository(ctx context.Context) error
	InstallPackages(ctx context.Context, packages []string) error
}
