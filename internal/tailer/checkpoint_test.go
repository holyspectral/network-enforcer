package tailer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckpointRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offsets.json")
	want := []savedCheckpoint{
		{Fingerprint: []byte("abc"), Offset: 10},
		{Fingerprint: []byte("def"), Offset: 20},
	}
	require.NoError(t, saveCheckpoints(path, want))

	got, ok := loadCheckpoints(path)
	require.True(t, ok)
	assert.Equal(t, want, got)

	// The write leaves no temporary file behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestLoadCheckpointsMissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "none.json")
	_, ok := loadCheckpoints(missing)
	assert.False(t, ok)

	corrupt := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(corrupt, []byte("{not json"), 0o600))
	_, ok = loadCheckpoints(corrupt)
	assert.False(t, ok)
}
