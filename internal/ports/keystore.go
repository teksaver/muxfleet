package ports

import (
	"context"
)

// Keystore defines the native workstation secrets storage port (Trousseau, Secret Service, Age) (AD-9, AD-10, R11, R23).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type Keystore interface {
	Name() string
	GetMasterKey(ctx context.Context) ([]byte, error)
	SetMasterKey(ctx context.Context, key []byte) error
	GetSecret(ctx context.Context, key string) (string, error)
	SetSecret(ctx context.Context, key string, value string) error
	DeleteSecret(ctx context.Context, key string) error
	HasSecret(ctx context.Context, key string) (bool, error)
}
