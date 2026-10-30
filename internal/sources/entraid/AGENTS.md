# AGENTS.md: internal/sources/entraid

The `entraid` source plugin. It reads Entra ID groups and their transitive user members from Microsoft Graph. Root context is in [AGENTS.md](../../../AGENTS.md).

## Files

| File | Responsibility |
| --- | --- |
| `entraid.go` | `New()` plugin setup: Azure credential and Graph client from env |
| `groups.go` | `ProcessGroups`: group query, pagination, and fan-out to org-specific teams |
| `users.go` | `ProcessUsers` and `processUserResult`: transitive member fetch and mapping |

## Authentication

- `azidentity.NewClientSecretCredential` using `CLIENT_ID`, `TENANT_ID`, and `CLIENT_SECRET` from the environment.
- Scope: `https://graph.microsoft.com/.default`.
- Setup failures in `New()` panic. This is existing behaviour.

## Group flow (`ProcessGroups`)

1. Build one request to `/groups` with `displayName in ('<name1>', '<name2>', ...)`, `$select=id,displayName,mail`, `$count=true`, and header `ConsistencyLevel: eventual`. The names come from `config.Instance.Teams.Names()`.
2. Follow `@odata.nextLink` until all pages are read.
3. `processGroupResult` for each group:
   - skip groups with no display name,
   - find every configuration entry for that name with `Teams.FindAll`. A group can sync into several Grafana orgs, with one team entry per key of `orgs` (`TargetOrgs()`),
   - set each `grafana.Team.Role` from `RoleFor(orgID)`,
   - fetch the group's users once with `ProcessUsers`, and reuse the list for each entry,
   - append one `grafana.Team` per entry,
   - remove the name from `config.Instance.Teams` so unmatched names can be reported.
4. Requested groups that Graph does not return are logged as not found.

Graph request errors currently panic. See the root [AGENTS.md](../../../AGENTS.md) follow-ups.

## User flow (`ProcessUsers`)

- Returns an empty list when `features.disableUserSync` is set.
- Requests `/groups/{id}/transitiveMembers/graph.user` with `$select=userPrincipalName,displayName,mail` and `$count=true`. Follows `@odata.nextLink`.
- `processUserResult` maps each user to `grafana.User{Login: UPN, Name: displayName, Email: lowercased mail}`:
  - user with no UPN: skipped (UPN is the Grafana login),
  - user with no mail: skipped,
  - user with no display name: kept, with an empty name.

## Testing

- `users_test.go` covers `processUserResult` with Graph model fixtures from `msgraph-sdk-go/models`. Nil fields are the main case.
- `processGroupResult` and `ProcessUsers` call Graph directly and have no unit tests. Covering them would need a Graph client fake or a wrapper interface. E2E tests (`-tags=e2e`) cover the full path with devproxy mocks in `test/devproxy/configs/entraid*.json`.

## Adding a source

New sources follow this pattern: a package under `internal/sources/<name>` that returns `*sourcetypes.SourcePlugin` and a `Process...` function producing `*grafana.Teams`. Register it in `sources.CallPlugin` and add its name to the source validator in `configtypes`.
