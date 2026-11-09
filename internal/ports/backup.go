package ports

import (
	"context"
	"time"

	"muxfleet/internal/domain"
)

// BackupSnapshot represents an existing restic backup snapshot (AD-17).
type BackupSnapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
	Hostname string    `json:"hostname"`
}

// Backup defines the backup and test-restore port (restic S3 backend) (AD-17).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type Backup interface {
	InitRepository(ctx context.Context) error
	CreateSnapshot(ctx context.Context, paths []string, tags []string) (string, error)
	VerifySnapshot(ctx context.Context, snapshotID string) (domain.Proof, error)
	TestRestore(ctx context.Context, snapshotID string, targetDir string) (domain.Proof, error)
	Restore(ctx context.Context, snapshotID string, targetDir string) error
	ListSnapshots(ctx context.Context) ([]BackupSnapshot, error)
}
