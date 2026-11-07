package fleetagent

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRunnerArchiveRejectsTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "runner.tar.gz")
	file, err := os.Create(archive)
	require.NoError(t, err)
	gz := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gz)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}))
	_, err = tarWriter.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, file.Close())
	err = extractRunnerArchive(archive, filepath.Join(t.TempDir(), "root"))
	assert.ErrorContains(t, err, "unsafe runner archive path")
}
