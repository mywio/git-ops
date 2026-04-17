package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanComposeEnvPersistenceRisksTreatsEnvironmentMapAsSafe(t *testing.T) {
	compose := `
services:
  api:
    image: ghcr.io/acme/api:latest
    environment:
      DATABASE_URL: ${DATABASE_URL}
`

	risks, err := scanComposeEnvPersistenceRisks(compose, []string{"DATABASE_URL=postgres://db"})

	require.NoError(t, err)
	assert.Empty(t, risks)
}

func TestScanComposeEnvPersistenceRisksTreatsEnvironmentListAsSafe(t *testing.T) {
	compose := `
services:
  api:
    image: ghcr.io/acme/api:latest
    environment:
      - APP_TOKEN
      - OTHER_VAR=${OTHER_VAR}
`

	risks, err := scanComposeEnvPersistenceRisks(compose, []string{"APP_TOKEN=secret", "OTHER_VAR=value"})

	require.NoError(t, err)
	assert.Empty(t, risks)
}

func TestScanComposeEnvPersistenceRisksWarnsForInterpolationOutsideRuntimeEnvironment(t *testing.T) {
	compose := `
services:
  api:
    image: ghcr.io/acme/api:${API_TOKEN}
`

	risks, err := scanComposeEnvPersistenceRisks(compose, []string{"API_TOKEN=secret"})

	require.NoError(t, err)
	require.Len(t, risks, 1)
	assert.Equal(t, composeEnvPersistenceRisk{
		Service: "api",
		Key:     "API_TOKEN",
		Reason:  "referenced in compose but not mapped into the service runtime environment",
	}, risks[0])
}

func TestScanComposeEnvPersistenceRisksGroupsMixedServices(t *testing.T) {
	compose := `
services:
  safe:
    image: ghcr.io/acme/safe:latest
    environment:
      SERVICE_TOKEN: ${SERVICE_TOKEN}
  risky:
    image: ghcr.io/acme/risky:${SERVICE_TOKEN}
    labels:
      - "build.secret=${BUILD_SECRET}"
`

	risks, err := scanComposeEnvPersistenceRisks(compose, []string{
		"SERVICE_TOKEN=secret",
		"BUILD_SECRET=build-secret",
	})

	require.NoError(t, err)
	assert.Equal(t, []composeEnvPersistenceRisk{
		{
			Service: "risky",
			Key:     "BUILD_SECRET",
			Reason:  "referenced in compose but not mapped into the service runtime environment",
		},
		{
			Service: "risky",
			Key:     "SERVICE_TOKEN",
			Reason:  "referenced in compose but not mapped into the service runtime environment",
		},
	}, risks)
}
