# AGENTS.md

Guidance for AI agents working in this repository. Keep this file and the per-package files in sync with code changes. User-facing docs live in [README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md); do not duplicate them here.

## Purpose

`grafana-oss-team-sync` gives members of Azure Entra ID (Azure AD) groups access to specific Grafana OSS organisations. It reads groups and their transitive user members from Microsoft Graph, then creates Grafana teams, syncs team membership, creates missing Grafana users, and grants folder permissions to teams. It is a single Go binary that runs as a one-shot sync.

## Runtime flow

`main.go` runs this sequence:

1. `flags.Load()`: parse CLI flags (`internal/flags`).
2. `config.Load()`: layer YAML config, `GOTS_*` environment variables, CLI flags, and an optional auth file (`internal/config`).
3. `grafana.New()`: build the Grafana API client, check health, and verify auth (`internal/grafana`).
4. `sources.CallPlugin()`: run the configured source plugin, which returns `*grafana.Teams`. It is skipped when no teams are configured (`internal/sources`).
5. `ProcessUsers()`: create missing global Grafana users (`internal/grafana/users.go`).
6. `ProcessTeams()`: create teams, then sync members (`internal/grafana/teams.go`).
7. `grafana.Instance.ProcessFolders(teams)`: create folders and overwrite their team permissions (`internal/grafana/folders.go`).
8. `grafana.Instance.DeleteUnlisted(configuredTeams, teams)`: only when `features.deleteUnlisted` is set. Deletes teams, top-level folders, and users that are not in the config or the source (`internal/grafana/unlisted.go`). `configuredTeams` is a copy taken before step 4, because the source processing removes matched names from `config.Instance.Teams`. With `features.dryRun` (default `true`) it only logs the planned deletions.

Only the `entraid` source plugin is implemented. New sources go under `internal/sources/<name>` and must be wired into `sources.CallPlugin`.

## Repository layout

| Path | Contents |
| --- | --- |
| `main.go` | Entry point, the runtime flow above |
| `internal/config/` | Config loading, validation, and typed config (see its AGENTS.md) |
| `internal/config/configtypes/` | Config structs (`Team`, `Folder`, `Grafana`, `Source`, ...) and validators |
| `internal/flags/` | CLI flag definitions |
| `internal/grafana/` | Grafana client, teams, folders, users (see its AGENTS.md) |
| `internal/sources/` | Source plugin dispatch and shared source types |
| `internal/sources/entraid/` | Microsoft Graph source (see its AGENTS.md) |
| `internal/helpers/` | Small generic helpers |
| `internal/tests/` | Harness for integration and e2e runs; mirrors `main.go` |
| `test/` | Test data (`test/data/`) and devproxy mocks (`test/devproxy/`) |
| `configs/config.yaml` | Example configuration |
| `deploy/` | Docker Compose stacks for local Grafana and integration tests |
| `build/package/Dockerfile`, `build/ci/.goreleaser.yaml` | Container and release build |
| `scripts/` | `tests.sh` (unit, coverage, integration, e2e), `container.sh` (lint, REUSE) |
| `.github/workflows/` | CI (`ci.yaml`), extended tests, release |

Generated or committed artefacts (`grafana-oss-team-sync` binary, `coverage.out`) are in the working tree. Do not commit new ones unless asked.

## Commands

```bash
go build ./...                                  # build
go test -race ./...                             # unit tests (default build tags)
scripts/tests.sh unit                           # unit tests with coverage.out
go test -tags=integration ./...                 # needs a running Grafana (deploy/)
SSL_CERT_FILE=... go test -tags=e2e ./...       # needs devproxy mocks; scripts/tests.sh e2e sets this up
scripts/container.sh go-lint                    # golangci-lint v2 in podman
scripts/container.sh licenses                   # REUSE lint in podman
gofmt -l .                                      # must print nothing
go vet ./...
```

Build tags: unit tests use `//go:build !integration && !e2e`. Integration tests use `integration`, and e2e tests use `e2e`. New unit test files must carry the unit tag so they do not run in the other suites.

## Conventions

- **License headers:** every new non-test source file needs the SPDX header used in existing files (`SPDX-FileCopyrightText` and `SPDX-License-Identifier: GPL-3.0-or-later`). `test/**` is exempt. REUSE is checked by `scripts/container.sh licenses`.
- **Commits:** the project requires DCO sign-off (`git commit -s`). Add the `Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>` trailer when the change is made with an agent.
- **Comments:** explain non-obvious behaviour only.
- **Logging:** use `log/slog`. Attach context with `slog.Group`, as in the existing code.
- **Errors:** `grafana` returns sentinel errors such as `ErrTeamNotFound` for "not found" cases. Callers should use `errors.Is`.
- **Dependencies:** Grafana API types come from `github.com/grafana/grafana-openapi-client-go`. Graph types come from `github.com/microsoftgraph/msgraph-sdk-go`. Do not hand-write API models.
- **Tests:** table-driven tests are the norm. Use `httptest` for Grafana API fakes (see `internal/grafana/lookup_test.go`).

## Known issues and open follow-ups

These are flagged, not fixed:

1. **Sync user role is Admin in non-default orgs.** `ensureSyncUserOrgMembership` in `internal/grafana/grafana.go` grants the basic-auth sync user the Admin role when it joins an org it switches into. The README documents this. Changing the role (for example making it configurable) is still open.
2. **Every team needs `orgs`.** A team must list at least one organization with a role. Plain strings and `orgId` are rejected at load time (`ErrTeamWithoutOrgs`), including `--teams` and `GOTS_TEAMS`. Roles are therefore enforced on every sync. A user who leaves a group keeps their org role in the normal sync. Users who are in no configured group are deleted from Grafana only with `deleteUnlisted=true` and `dryRun=false` (`deleteUnlistedUsers`). This is opt-in, and the zero-users guard stays on.
3. **Nested folders are unverified.** `searchFolder` lists folders by title. Behaviour with nested folders (`parentUid`) has not been tested against a real Grafana.
4. **Graph filter length is unverified.** `ProcessGroups` builds a `displayName in (...)` filter from all configured team names. Very long lists may hit URL limits.
5. **Graph errors panic.** `entraid` panics on Graph call errors instead of returning them. This is existing behaviour.
6. **Group ID dereference.** `processGroupResult` dereferences `group.GetId()` without a nil check. Graph always returns IDs for selected groups, so this is low risk.
