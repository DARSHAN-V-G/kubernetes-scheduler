package action

import (
	"fmt"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/storage"
	"go.uber.org/zap"
)

// CheckpointValidator validates tarball integrity, checks descriptors, and calculates cryptographic digests.
type CheckpointValidator struct {
	storage storage.CheckpointStorage
	logger  *zap.Logger
}

// NewCheckpointValidator constructs a validator using the given storage implementation.
func NewCheckpointValidator(store storage.CheckpointStorage, logger *zap.Logger) *CheckpointValidator {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &CheckpointValidator{
		storage: store,
		logger:  logger,
	}
}

// ValidateArchive checks whether the archive exists, is not empty, can be read as a tar,
// and computes its SHA-256 hash.
func (v *CheckpointValidator) ValidateArchive(path string) (*storage.ArchiveMetadata, error) {
	if path == "" {
		return nil, fmt.Errorf("empty archive path")
	}

	exists, err := v.storage.Exists(path)
	if err != nil {
		return nil, fmt.Errorf("storage access error checking archive: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("checkpoint archive does not exist at %s", path)
	}

	meta, err := v.storage.VerifyArchive(path)
	if err != nil {
		return nil, fmt.Errorf("checkpoint archive validation failed for %s: %w", path, err)
	}

	v.logger.Info("Checkpoint archive successfully validated",
		zap.String("path", meta.Path),
		zap.Int64("sizeBytes", meta.SizeBytes),
		zap.String("sha256", meta.ChecksumSHA),
		zap.Int("fileCount", meta.FileCount),
	)

	return meta, nil
}
