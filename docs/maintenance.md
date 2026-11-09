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

## Releases

Release Please opens a version and changelog PR after conventional commits such
as `fix:` land on `master`. Merge that PR to create a draft release and start
the artifact job. The tag is created with the draft so Release Please can find
the previous version while assets are building. The job checks out the release
commit, builds and attests the artifacts, uploads all five release assets, and
then publishes the release.
Publication makes the release immutable, so the release stays a draft until
every build, attestation, and upload step succeeds.

If an artifact step fails, resolve the failure and retry the failed job while
the release is still a draft:

```bash
gh run rerun <run-id> --failed
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
