package storage

import (
	"time"
)

// ArchiveMetadata contains properties and cryptographic integrity details of a checkpoint archive.
type ArchiveMetadata struct {
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"sizeBytes"`
	ChecksumSHA string    `json:"checksumSha"`
	FileCount   int       `json:"fileCount"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CheckpointStorage defines the interface for interacting with checkpoint archives
// on local worker filesystems, shared network volumes (PVCs), or object stores.
type CheckpointStorage interface {
	// Exists checks if an archive file exists at the given path.
	Exists(path string) (bool, error)

	// GetSize returns the byte size of an archive.
	GetSize(path string) (int64, error)

	// ComputeSHA256 calculates the cryptographic SHA-256 digest of the archive.
	ComputeSHA256(path string) (string, error)

	// VerifyArchive inspects the archive structure, confirming it is a valid tar file.
	VerifyArchive(path string) (*ArchiveMetadata, error)

	// Delete removes a checkpoint archive from storage.
	Delete(path string) error
}
