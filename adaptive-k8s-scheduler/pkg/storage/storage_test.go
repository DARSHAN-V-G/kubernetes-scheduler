package storage

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func createTestTar(t *testing.T, dir, filename string, contents map[string]string) string {
	t.Helper()
	tarPath := filepath.Join(dir, filename)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for name, body := range contents {
		hdr := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("failed to write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("failed to write tar content: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}

	if err := os.WriteFile(tarPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write test tar file: %v", err)
	}

	return tarPath
}

func TestLocalFSStorage(t *testing.T) {
	tempDir := t.TempDir()
	store := NewLocalFSStorage(tempDir)

	// Test non-existent file
	exists, err := store.Exists("nonexistent.tar")
	if err != nil {
		t.Fatalf("Exists returned unexpected error: %v", err)
	}
	if exists {
		t.Errorf("Expected exists=false for nonexistent file")
	}

	// Create test tar archive
	testFiles := map[string]string{
		"checkpoint/descriptors.json": `{"version": 1}`,
		"checkpoint/pages-1.img":       "mock-memory-page-data",
	}
	tarPath := createTestTar(t, tempDir, "test-ckpt.tar", testFiles)

	// Test Exists
	exists, err = store.Exists(tarPath)
	if err != nil || !exists {
		t.Errorf("Expected exists=true, got %v, err=%v", exists, err)
	}

	// Test GetSize
	size, err := store.GetSize(tarPath)
	if err != nil || size <= 0 {
		t.Errorf("Expected positive size, got %d, err=%v", size, err)
	}

	// Test ComputeSHA256
	hash, err := store.ComputeSHA256(tarPath)
	if err != nil || len(hash) != 64 {
		t.Errorf("Expected 64-char hex SHA256, got '%s', err=%v", hash, err)
	}

	// Test VerifyArchive
	meta, err := store.VerifyArchive(tarPath)
	if err != nil {
		t.Fatalf("VerifyArchive failed: %v", err)
	}
	if meta.FileCount != 2 {
		t.Errorf("Expected 2 files in tar, got %d", meta.FileCount)
	}
	if meta.ChecksumSHA != hash {
		t.Errorf("Checksum mismatch between ComputeSHA256 and VerifyArchive: %s != %s", hash, meta.ChecksumSHA)
	}

	// Test Delete
	if err := store.Delete(tarPath); err != nil {
		t.Errorf("Failed to delete archive: %v", err)
	}
	exists, _ = store.Exists(tarPath)
	if exists {
		t.Errorf("Expected file to be deleted")
	}
}

func TestCorruptedArchiveVerification(t *testing.T) {
	tempDir := t.TempDir()
	store := NewLocalFSStorage(tempDir)

	// Empty file test
	emptyFile := filepath.Join(tempDir, "empty.tar")
	if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyArchive(emptyFile); err == nil {
		t.Errorf("Expected error verifying 0-byte archive, got nil")
	}

	// Corrupt tar header test
	corruptFile := filepath.Join(tempDir, "corrupt.tar")
	if err := os.WriteFile(corruptFile, []byte("garbage data that is not a tar header"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyArchive(corruptFile); err == nil {
		t.Errorf("Expected error verifying corrupt archive, got nil")
	}
}
