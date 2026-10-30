# AGENTS.md: internal/config

Configuration loading, validation, and typed config. Root context is in [AGENTS.md](../../AGENTS.md).

## Files

| File | Responsibility |
| --- | --- |
| `config.go` | `Load()`, layering, decode hook, validation calls, `Instance` singleton |
| `configtypes/config.go` | Top-level `Config` struct |
| `configtypes/grafana.go` | `Grafana` settings, auth type and scheme validators |
| `configtypes/source.go` | `Source` settings, `ValidateSourcePlugin` |
| `configtypes/teams.go` | `Team{Name, Orgs}`, `TargetOrgs` (sorted org IDs), `RoleFor`, `OrgRole` (`Viewer`/`Editor`/`Admin`), `ErrTeamWithoutOrgs`, `ValidateTeamOrgs`, and `Teams` helpers |
| `configtypes/folders.go` | `Folder` and folder permissions |
| `configtypes/features.go` | Feature toggles such as `DisableUserSync`, `addLocalAdminToTeams`, `deleteUnlisted` (default `false`), and `dryRun` (default `true`) |

## Load order

`Load()` applies these layers. Later layers override earlier ones:

1. YAML config file (required). Default `configs/config.yaml` in the examples.
2. Environment variables with the `GOTS_` prefix.
3. CLI flags (posflag). Unchanged flag defaults do not overwrite values already set by YAML or env. This was verified against `providers/posflag` v1.0.2.
4. Optional auth file (`GOTS_AUTHFILE`, godotenv). It does not override variables already set in the environment.

A `.env` file in the working directory is loaded automatically.

Decoding uses mapstructure with `WeaklyTypedInput` and a `teamStringDecodeHook`. Every team must be a structured entry with `name` and `orgs`. The hook rejects plain strings with `ErrTeamWithoutOrgs`.

The `teams` flag and `GOTS_TEAMS` env var are comma-separated names, so they produce plain strings. `splitTeamNames` splits on commas, trims whitespace and drops empty entries. Without it, an empty default became a phantom team named `""`.

## Validation

Validation runs after decoding and fails fast:

- auth type must be `token` or `basicauth`,
- scheme must be a valid Grafana scheme,
- `ValidateTeamOrgs`: a team must have at least one `orgs` entry (`ErrTeamWithoutOrgs`). `orgs` keys must be greater than 0 and roles must be valid. Role names are canonicalized (for example `editor` becomes `Editor`).
- Token auth vs. org scope is checked in `grafana.Init` (`validateTokenOrgs`), not here. The token's org ID is only known after `GetCurrentOrg`.
- source plugin must be a known plugin (currently `entraid`).

When adding a new option, add its validator in `configtypes` and a test next to the existing ones (`grafana_test.go`, `source_test.go`).

## Conventions

- `config.Instance` is the global typed config. Other packages read it. Only `Load()` writes it, and tests may replace it.
- Secrets (tokens, passwords, client secrets) are read from environment or auth file. Do not store them in `Instance`.
- Naming: validator functions use `Validate<Thing>`. This was renamed from a `Valdidate` typo; do not reintroduce it.
