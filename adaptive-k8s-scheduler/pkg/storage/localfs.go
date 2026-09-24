package storage

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalFSStorage implements CheckpointStorage for local filesystem paths
// (e.g. /var/lib/kubelet/checkpoints or mounted persistent volumes).
type LocalFSStorage struct {
	baseDir string
}

// NewLocalFSStorage initializes a new local filesystem storage handler.
// If baseDir is empty, default "/var/lib/kubelet/checkpoints" is assumed.
func NewLocalFSStorage(baseDir string) *LocalFSStorage {
	if baseDir == "" {
		baseDir = "/var/lib/kubelet/checkpoints"
	}
	return &LocalFSStorage{
		baseDir: filepath.Clean(baseDir),
	}
}

// BaseDir returns the root checkpoints directory.
func (s *LocalFSStorage) BaseDir() string {
	return s.baseDir
}

// Exists checks if an archive file exists at the given path.
func (s *LocalFSStorage) Exists(path string) (bool, error) {
	fullPath := s.resolvePath(path)
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check existence of %s: %w", fullPath, err)
	}
	return !info.IsDir(), nil
}

// GetSize returns the byte size of an archive file.
func (s *LocalFSStorage) GetSize(path string) (int64, error) {
	fullPath := s.resolvePath(path)
	info, err := os.Stat(fullPath)
	if err != nil {
		return 0, fmt.Errorf("failed to stat file %s: %w", fullPath, err)
	}
	return info.Size(), nil
}

// ComputeSHA256 streams the file contents and calculates its cryptographic SHA-256 hash.
func (s *LocalFSStorage) ComputeSHA256(path string) (string, error) {
	fullPath := s.resolvePath(path)
	file, err := os.Open(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s for hashing: %w", fullPath, err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to stream hash for %s: %w", fullPath, err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// VerifyArchive inspects the archive structure, confirming it is a valid non-empty tar file,
// and returns metadata including file count and checksum.
func (s *LocalFSStorage) VerifyArchive(path string) (*ArchiveMetadata, error) {
	fullPath := s.resolvePath(path)

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("archive stat failed for %s: %w", fullPath, err)
	}

	if info.Size() == 0 {
		return nil, fmt.Errorf("archive %s is empty (0 bytes)", fullPath)
	}

	file, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive %s: %w", fullPath, err)
	}
	defer file.Close()

	// Compute hash and read tar entries simultaneously via TeeReader
	hasher := sha256.New()
	tee := io.TeeReader(file, hasher)
	tarReader := tar.NewReader(tee)

	fileCount := 0
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("corrupted tar archive at entry %d (%s): %w", fileCount, fullPath, err)
		}
		if header != nil {
			fileCount++
		}
	}

	// Flush any remaining trailing data for the hash
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, fmt.Errorf("failed completing hash stream for %s: %w", fullPath, err)
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))

	return &ArchiveMetadata{
		Path:        fullPath,
		SizeBytes:   info.Size(),
		ChecksumSHA: checksum,
		FileCount:   fileCount,
		CreatedAt:   info.ModTime(),
	}, nil
}

// Delete removes a checkpoint archive from disk.
func (s *LocalFSStorage) Delete(path string) error {
	fullPath := s.resolvePath(path)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete checkpoint %s: %w", fullPath, err)
	}
	return nil
}

// resolvePath ensures paths are treated either as absolute paths or relative to baseDir.
func (s *LocalFSStorage) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(s.baseDir, filepath.Clean(path))
}
