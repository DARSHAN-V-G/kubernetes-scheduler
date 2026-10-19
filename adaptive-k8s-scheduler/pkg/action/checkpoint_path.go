package action

import "os"

const defaultCheckpointDir = "/var/lib/kubelet/checkpoints"

// CheckpointDirectory returns the directory visible to the scheduler process.
func CheckpointDirectory() string {
	if directory := os.Getenv("CHECKPOINT_DIR"); directory != "" {
		return directory
	}
	return defaultCheckpointDir
}