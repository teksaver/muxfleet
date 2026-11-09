package ports

import (
	"context"
)

// CodingCLI defines the interface for supported AI coding CLIs (opencode, claude, agy, codex) (AD-12).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type CodingCLI interface {
	Name() string
	RequiresAuth() bool
	IsInstalled(ctx context.Context) (bool, error)
	Authenticate(ctx context.Context) (authURL string, err error)
	Launch(ctx context.Context, projectDir string, args []string) error
}
