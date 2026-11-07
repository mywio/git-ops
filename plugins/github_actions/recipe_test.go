package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseActionsManifestStrict(t *testing.T) {
	manifest, err := parseActionsManifest([]byte(`version: 1
deployments:
  - id: production
    name: Production
    workflow: .github/workflows/deploy.yml
    environment: production
    runner_labels: [self-hosted, ghops, server-1]
`))
	require.NoError(t, err)
	require.Len(t, manifest.Deployments, 1)
	assert.Equal(t, ".github/workflows/deploy.yml", manifest.Deployments[0].Workflow)
}

func TestParseActionsManifestRejectsUnknownAndTraversal(t *testing.T) {
	_, err := parseActionsManifest([]byte("version: 1\nunknown: true\ndeployments: []\n"))
	assert.Error(t, err)
	_, err = parseActionsManifest([]byte(`version: 1
deployments:
  - id: production
    workflow: ../deploy.yml
    runner_labels: [self-hosted]
`))
	assert.ErrorContains(t, err, "invalid workflow path")
}
