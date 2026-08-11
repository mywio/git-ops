# Maintenance

This document covers maintainer-oriented verification workflows that are useful
for ongoing repository health but are not part of the end-user quick-start
experience.

## Mutation Testing

Mutation testing runs in a separate GitHub Actions workflow so it does not slow
down normal CI.

- Trigger it manually from the `Mutation Testing` workflow in GitHub Actions
- It also runs weekly on Monday
- Reports are uploaded as workflow artifacts, one per target package

The current mutation workflow covers:

- `./pkg/core`
- `./pkg/config`
- `./pkg/utils`
- `./plugins/mcp`
- `./plugins/reconciler`

The workflow configuration lives in:

- [`.github/workflows/mutation.yml`](../.github/workflows/mutation.yml)
- [`.mutesting.yml`](../.mutesting.yml)

## Local Verification

Useful local verification commands:

```bash
go test -count=1 ./...
go test -race ./...
go test -cover ./...
```

## SonarQube

The repository includes a root
[`sonar-project.properties`](../sonar-project.properties) file for SonarQube
or SonarScanner CLI runs.

Typical local flow:

```bash
go test -covermode=atomic -coverprofile=coverage.out ./...
sonar-scanner
```

The config is tuned for this repository shape:

- analyzes the whole repo from the `git-ops/` root
- treats `*_test.go` files as tests
- excludes generated or runtime-only paths such as:
  - `.gocache/`
  - `bin/`
  - `plugins/ui/frontend/dist/`
  - `plugins/mcp/docs/`

Remove `coverage.out` after the scan if you do not want to keep it locally.

Short fuzz runs can be used to sanity-check parser-heavy code paths:

```bash
go test ./pkg/config -run=^$ -fuzz=FuzzSplitAndTrim -fuzztime=3s
go test ./pkg/config -run=^$ -fuzz=FuzzLoadConfigFromMap -fuzztime=3s
go test ./plugins/reconciler -run=^$ -fuzz=FuzzParseComposePSOutput -fuzztime=3s
go test ./plugins/reconciler -run=^$ -fuzz=FuzzScanComposeEnvPersistenceRisks -fuzztime=3s
go test ./plugins/mcp -run=^$ -fuzz=FuzzParseHealthContainers -fuzztime=3s
```
