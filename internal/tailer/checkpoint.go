package tailer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// savedCheckpoint is one offset record on disk.
type savedCheckpoint struct {
	Fingerprint []byte    `json:"fingerprint"`
	Offset      int64     `json:"offset"`
	LastSeen    time.Time `json:"lastSeen"`
}

// loadCheckpoints reads the offset file. It returns false when the file is
// absent or corrupt, so the caller starts at the end.
func loadCheckpoints(path string) ([]savedCheckpoint, bool) {
	if path == "" {
		return nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var recs []savedCheckpoint
	if unmarshalErr := json.Unmarshal(raw, &recs); unmarshalErr != nil {
		return nil, false
	}
	return recs, true
}

// saveCheckpoints writes the offset file atomically. It writes a temporary file,
// syncs it, renames it and syncs the directory.
func saveCheckpoints(path string, recs []savedCheckpoint) error {
	if path == "" {
		return nil
	}
	raw, err := json.Marshal(recs)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, writeErr := f.Write(raw); writeErr != nil {
		closeErr := f.Close()
		if closeErr != nil {
			return closeErr
		}
		return writeErr
	}
	if syncErr := f.Sync(); syncErr != nil {
		closeErr := f.Close()
		if closeErr != nil {
			return closeErr
		}
		return syncErr
	}
	if closeErr := f.Close(); closeErr != nil {
		return closeErr
	}
	if renameErr := os.Rename(tmp, path); renameErr != nil {
		return renameErr
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}
