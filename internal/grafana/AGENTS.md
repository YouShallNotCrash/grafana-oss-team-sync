# AGENTS.md: internal/grafana

Grafana OSS client and sync logic. Teams, folders, and users are managed here. Read the root [AGENTS.md](../../AGENTS.md) first for the overall flow.

## Files

| File | Responsibility |
| --- | --- |
| `grafana.go` | `New()` client setup, `Instance` singleton, auth, `EnsureOrgContext`, sync-user org membership |
| `teams.go` | `Team` type, `ProcessTeams`, paginated exact-name lookup (`findTeam`), create, membership sync |
| `folders.go` | `Folder` type, `ProcessFolders`, paginated title search, permission overwrite |
| `unlisted.go` | `DeleteUnlisted`: deletes teams, top-level folders, and users not in the config or source. Dry-run aware |
| `users.go` | `User`/`Users` types, `ProcessUsers` (dedupe and create global users) |
| `*_test.go` | Unit tests. `lookup_test.go` uses `httptest` to fake the Grafana API |

## Client and auth

- `New()` builds the OpenAPI client from `config.Instance.Grafana`. It runs a health check and `GetCurrentOrg`, so bad credentials fail fast.
- Auth types:
  - `token`: a service account or API token from `GOTS_TOKEN` or `--token`, sent as the API key.
  - `basicauth`: `GOTS_USERNAME` and `GOTS_PASSWORD`, or `-u` and `-p`. This is the only type that can switch orgs.
- Credentials are kept out of `config.Instance`. Do not add them there.
- For basic auth, `New()` also reads the sync user's login from `SignedInUser`. This is used to manage its org membership.

## Org context

Grafana's team and folder APIs act on the caller's **current** org. They take no per-request org parameter, and OSS ignores `X-Grafana-Org-Id` for basic auth. So:

- `EnsureOrgContext(orgID)` switches the sync user's current org with `SignedInUser.UserSetUsingOrg`.
- `orgID == 0` means the default org (`defaultOrgID`).
- It returns early when the target equals `currentOrgID`.
- A non-zero org requires basic auth. `config` validates this.
- Every team and folder operation calls `api()`, which calls `EnsureOrgContext` first. New operations must do the same.

## Team sync semantics

`ProcessTeams` for each `Team`:

1. Find the team by **exact, case-sensitive** name (`findTeam`). Grafana's search is not trusted for exact matches, so `findTeam` pages through results (1000 per page, stopping on a short page) and compares names itself.
2. Create the team if it is missing.
3. If user sync is enabled, `addUsersToTeam`:
   - optionally adds `admin@localhost` (`addLocalAdminToTeams`, default true),
   - ensures each user is an org member, see **Organization roles** below,
   - calls `SetTeamMemberships` with Admins and Members. **This fully replaces** the team's membership, so manual changes in Grafana are overwritten.

`getTeamID` returns `ErrTeamNotFound` when the lookup finds nothing. Callers must handle it, not index into results.

## Organization roles

`ensureOrgMembership(loginOrEmail, desiredRole)` runs for every org-scoped team (`OrgID != 0`). It is skipped for the default org.

- `Team.Role` is set by the source (`groups.go`) from the team's `orgs` config (`RoleFor(orgID)`). Every config team has `orgs`, so the role is always set.
- `ProcessTeams` builds `desiredOrgRoles` once: for each `{orgID, lowercase email}` the highest-ranked configured role across all teams. Users with no email are skipped.
- A user missing from the org is added with the desired role, or `Viewer` when there is none.
- An existing member gets `UpdateOrgUser` only when a role is configured and differs. An empty role never changes an existing member, so implicit entries never downgrade.
- The `admin` and sync user logins (`isProtectedLogin`) are never changed.
- Removing a user from a group does not revoke the org role. Only team membership is removed.

## Folder semantics

- The folder UID is the lowercased map key from config.
- `searchFolder` pages through `/folders` in pages of `folderPageSize` (1000) and stops on a short page.
- `manageFolderPermissions` **overwrites all** folder permissions with the configured teams and roles. An empty list clears them. Roles are a bitmask: Viewer=1, Editor=2, Admin=4.

## Users

- `ProcessUsers` dedupes users across teams (whole-value comparison of login, name, and email) and creates missing global users with `AdminCreateUser` and a random 32-character password.
- Token auth cannot create users. The code logs a warning and skips this step.

## Deleting unlisted resources

`DeleteUnlisted` runs only when `features.deleteUnlisted` is true. The source of truth is the config plus the source plugin output for this run. Nothing is tracked between runs.

- Teams: deleted in every org referenced by config `teams`, when not in the config list. Matching is case-insensitive. The config list passed in must be the snapshot taken before source processing.
- Folders: deleted in every org referenced by config `folders`, when the top-level title is not in the config. Non-forced delete, so folders with alert rules fail and are logged. Subfolders go with their parent.
- Users: deleted when not returned by the source. Runs only with basic auth, user sync enabled, and at least one source user. Protected: `IsAdmin` server admins, login `admin`, the sync user login, and `admin@localhost`.
- Dry run (`features.dryRun`, default `true`) logs "would delete" and makes no delete calls.
- Team membership is not reconciled here. Membership is handled by `ProcessTeams`.


`ensureSyncUserOrgMembership` adds the basic-auth sync user as **Admin** to each non-default org that the tool switches into, when it is not already a member. The default org is skipped. This is documented in the README's Opinionated Behaviour section.

## Testing

- Unit tests fake Grafana with `httptest.NewServer` and set `Instance` directly (`newTestInstance` in `lookup_test.go`). Reset `Instance` in cleanup.
- Guard any state shared between the handler and the test with a mutex. Tests run with `-race`.
- Integration tests in `internal/tests` run against real Grafana versions (see `deploy/`).
