package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileForwarderPlugin_GetRuntimeFiles(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "cert.pem")
	err := os.WriteFile(source, []byte("cert-data"), 0600)
	require.NoError(t, err)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(map[string]map[string]any{
		"file_forwarder": {
			"files": []map[string]any{
				{
					"env":      "TLS_CERT_FILE",
					"path":     source,
					"filename": "tls.crt",
					"mode":     "0600",
				},
			},
		},
	})

	p := &FileForwarderPlugin{}
	err = p.Init(context.Background(), logger, mgr)
	require.NoError(t, err)

	res, err := p.Execute(context.Background(), "get_runtime_files", map[string]interface{}{})
	require.NoError(t, err)

	files, ok := res.([]core.RuntimeFile)
	require.True(t, ok)
	require.Len(t, files, 1)
	assert.Equal(t, "TLS_CERT_FILE", files[0].EnvKey)
	assert.Equal(t, "tls.crt", files[0].Filename)
	assert.Equal(t, uint32(0600), files[0].Mode)
	assert.Equal(t, []byte("cert-data"), files[0].Content)

	statsRes, err := p.Execute(context.Background(), "get_stats", map[string]interface{}{})
	require.NoError(t, err)
	stats := statsRes.(fileForwarderStatsView)
	assert.Equal(t, 1, stats.ConfiguredFiles)
	assert.Equal(t, 1, stats.ForwardedFiles)
	assert.Empty(t, stats.MissingFiles)
}

func TestFileForwarderPlugin_MissingOptionalFile(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(map[string]map[string]any{
		"file_forwarder": {
			"files": []map[string]any{
				{
					"env":      "OPTIONAL_FILE",
					"path":     "/does/not/exist",
					"required": false,
				},
			},
		},
	})

	p := &FileForwarderPlugin{}
	err := p.Init(context.Background(), logger, mgr)
	require.NoError(t, err)

	res, err := p.Execute(context.Background(), "get_runtime_files", map[string]interface{}{})
	require.NoError(t, err)
	files := res.([]core.RuntimeFile)
	assert.Len(t, files, 0)
}

func TestFileForwarderPlugin_MissingRequiredFile(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(map[string]map[string]any{
		"file_forwarder": {
			"files": []map[string]any{
				{
					"env":      "REQUIRED_FILE",
					"path":     "/does/not/exist",
					"required": true,
				},
			},
		},
	})

	p := &FileForwarderPlugin{}
	err := p.Init(context.Background(), logger, mgr)
	require.NoError(t, err)

	_, err = p.Execute(context.Background(), "get_runtime_files", map[string]interface{}{})
	assert.Error(t, err)
}
