package engine

// Re-exports for the archive folder and its lock, so the UI packages reach
// internal/archive only through engine.

import (
	"context"

	"github.com/TTox-Tech/chitragit/internal/archive"
)

type (
	Archive     = archive.Archive
	ArchiveLock = archive.Lock
	LockedError = archive.LockedError
)

var (
	ErrNoArchive = archive.ErrNotFound
)

func FindArchive(start string) (*archive.Archive, error) { return archive.Find(start) }

func InitArchive(ctx context.Context, root string) (*archive.Archive, bool, error) {
	return archive.Init(ctx, root)
}

func OpenArchive(root string) (*archive.Archive, error) { return archive.Open(root) }
