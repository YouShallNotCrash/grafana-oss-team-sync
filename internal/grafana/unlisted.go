// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package grafana

import (
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/grafana/grafana-openapi-client-go/client/folders"
	"github.com/grafana/grafana-openapi-client-go/client/teams"
	"github.com/grafana/grafana-openapi-client-go/client/users"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/skuethe/grafana-oss-team-sync/internal/config"
	"github.com/skuethe/grafana-oss-team-sync/internal/config/configtypes"
)

const (
	// userPageSize is the number of users requested per page when listing all users.
	userPageSize int64 = 1000

	// Accounts that are never deleted, regardless of the source data.
	protectedAdminLogin = "admin"
	protectedAdminEmail = "admin@localhost"
)

// normalizeOrgID maps the unset organization (0) to the one the configured credentials default to.
func (g *GrafanaInstance) normalizeOrgID(orgID int64) int64 {
	if orgID == 0 {
		return g.defaultOrgID
	}
	return orgID
}

// DeleteUnlisted makes Grafana match the configuration and the source data by removing everything
// that is no longer part of them. It only runs when features.deleteUnlisted is enabled, and only
// logs the planned deletions when features.dryRun is enabled.
//
// configuredTeams must be the team list from the configuration before the source plugin ran,
// because processing the source removes matched names from config.Instance.Teams.
// syncedTeams is the team list produced by the source plugin, and is used to determine the desired users.
//
// Scope:
//   - teams and folders are only deleted in organizations referenced by the configuration
//   - users are only deleted when user sync is enabled, basic auth is used and the source produced at least one user
func (g *GrafanaInstance) DeleteUnlisted(configuredTeams configtypes.Teams, syncedTeams *Teams) {
	unlistedLog := slog.With(slog.String("package", "grafana.unlisted"))

	if !config.Instance.Features.DeleteUnlisted {
		unlistedLog.Info("deleteUnlisted feature disabled, skipping")
		return
	}

	dryRun := config.Instance.Features.DryRun
	unlistedLog.Info("processing unlisted Grafana resources",
		slog.Bool("dryRun", dryRun),
	)

	g.deleteUnlistedTeams(configuredTeams, dryRun)
	g.deleteUnlistedFolders(dryRun)
	g.deleteUnlistedUsers(syncedTeams, dryRun)

	unlistedLog.Info("finished processing unlisted Grafana resources",
		slog.Bool("dryRun", dryRun),
	)
}

// listTeams returns all teams in the given organization.
func (g *GrafanaInstance) listTeams(orgID int64) ([]*models.TeamDTO, error) {
	if err := g.EnsureOrgContext(orgID); err != nil {
		return nil, err
	}

	var all []*models.TeamDTO
	for page := int64(1); ; page++ {
		limit := teamPageSize
		pageNumber := page
		result, err := g.api.Teams.SearchTeams(&teams.SearchTeamsParams{
			Page:    &pageNumber,
			Perpage: &limit,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, result.Payload.Teams...)
		if int64(len(result.Payload.Teams)) < teamPageSize {
			return all, nil
		}
	}
}

// deleteUnlistedTeams deletes every team in an organization referenced by the configured teams
// whose name is not configured for that organization.
func (g *GrafanaInstance) deleteUnlistedTeams(configuredTeams configtypes.Teams, dryRun bool) {
	teamsLog := slog.With(slog.String("package", "grafana.unlisted.teams"))

	desiredByOrg := map[int64][]string{}
	for _, team := range configuredTeams {
		for _, targetOrgID := range team.TargetOrgs() {
			orgID := g.normalizeOrgID(targetOrgID)
			desiredByOrg[orgID] = append(desiredByOrg[orgID], team.Name)
		}
	}

	countDeleted := 0
	countPlanned := 0
	countFailed := 0

	for _, orgID := range slices.Sorted(maps.Keys(desiredByOrg)) {
		orgLog := teamsLog.With(slog.Int64("orgId", orgID))

		existing, err := g.listTeams(orgID)
		if err != nil {
			orgLog.Error("could not list teams", slog.Any("error", err))
			countFailed++
			continue
		}

		desired := desiredByOrg[orgID]
		for _, team := range existing {
			if team.ID == nil || team.Name == nil {
				orgLog.Warn("skipping team without id or name")
				continue
			}
			if slices.ContainsFunc(desired, func(name string) bool { return strings.EqualFold(name, *team.Name) }) {
				continue
			}

			teamLog := orgLog.With(
				slog.Group("team",
					slog.String("name", *team.Name),
					slog.Int64("id", *team.ID),
				),
			)

			if dryRun {
				teamLog.Info("dry run: would delete unlisted team")
				countPlanned++
				continue
			}

			if _, err := g.api.Teams.DeleteTeamByID(strconv.FormatInt(*team.ID, 10)); err != nil {
				teamLog.Error("could not delete unlisted team", slog.Any("error", err))
				countFailed++
				continue
			}
			teamLog.Info("deleted unlisted team")
			countDeleted++
		}
	}

	teamsLog.Info("finished processing unlisted teams",
		slog.Group("teams",
			slog.Int("deleted", countDeleted),
			slog.Int("planned", countPlanned),
			slog.Int("failed", countFailed),
		),
	)
}

// listTopLevelFolders returns all top-level folders in the given organization.
func (g *GrafanaInstance) listTopLevelFolders(orgID int64) ([]*models.FolderSearchHit, error) {
	if err := g.EnsureOrgContext(orgID); err != nil {
		return nil, err
	}

	var all []*models.FolderSearchHit
	for page := int64(1); ; page++ {
		limit := folderPageSize
		pageNumber := page
		result, err := g.api.Folders.GetFolders(folders.NewGetFoldersParams().WithLimit(&limit).WithPage(&pageNumber))
		if err != nil {
			return nil, err
		}
		all = append(all, result.Payload...)
		if int64(len(result.Payload)) < folderPageSize {
			return all, nil
		}
	}
}

// deleteUnlistedFolders deletes every top-level folder in an organization referenced by the configured
// folders whose title is not configured for that organization. Folders that still contain alert rules
// are not force-deleted, so they are reported as errors instead.
func (g *GrafanaInstance) deleteUnlistedFolders(dryRun bool) {
	foldersLog := slog.With(slog.String("package", "grafana.unlisted.folders"))

	desiredByOrg := map[int64][]string{}
	for _, folder := range config.Instance.Folders {
		orgID := g.normalizeOrgID(folder.OrgID)
		desiredByOrg[orgID] = append(desiredByOrg[orgID], folder.Title)
	}

	countDeleted := 0
	countPlanned := 0
	countFailed := 0

	for _, orgID := range slices.Sorted(maps.Keys(desiredByOrg)) {
		orgLog := foldersLog.With(slog.Int64("orgId", orgID))

		existing, err := g.listTopLevelFolders(orgID)
		if err != nil {
			orgLog.Error("could not list folders", slog.Any("error", err))
			countFailed++
			continue
		}

		desired := desiredByOrg[orgID]
		for _, folder := range existing {
			if folder.UID == "" {
				orgLog.Warn("skipping folder without uid", slog.String("title", folder.Title))
				continue
			}
			if slices.Contains(desired, folder.Title) {
				continue
			}

			folderLog := orgLog.With(
				slog.Group("folder",
					slog.String("uid", folder.UID),
					slog.String("title", folder.Title),
				),
			)

			if dryRun {
				folderLog.Info("dry run: would delete unlisted folder")
				countPlanned++
				continue
			}

			if _, err := g.api.Folders.DeleteFolder(folders.NewDeleteFolderParams().WithFolderUID(folder.UID)); err != nil {
				folderLog.Error("could not delete unlisted folder", slog.Any("error", err))
				countFailed++
				continue
			}
			folderLog.Info("deleted unlisted folder")
			countDeleted++
		}
	}

	foldersLog.Info("finished processing unlisted folders",
		slog.Group("folders",
			slog.Int("deleted", countDeleted),
			slog.Int("planned", countPlanned),
			slog.Int("failed", countFailed),
		),
	)
}

// listAllUsers returns every user known to Grafana.
func (g *GrafanaInstance) listAllUsers() ([]*models.UserSearchHitDTO, error) {
	var all []*models.UserSearchHitDTO
	for page := int64(1); ; page++ {
		limit := userPageSize
		pageNumber := page
		result, err := g.api.Users.SearchUsers(&users.SearchUsersParams{
			Page:    &pageNumber,
			Perpage: &limit,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, result.Payload...)
		if int64(len(result.Payload)) < userPageSize {
			return all, nil
		}
	}
}

// isProtectedLogin reports whether a login belongs to an account the sync must never modify or delete.
func (g *GrafanaInstance) isProtectedLogin(login string) bool {
	return login != "" && (strings.EqualFold(login, protectedAdminLogin) || strings.EqualFold(login, g.syncUserLogin))
}

// isProtectedUser reports whether a user must never be deleted by the sync.
func (g *GrafanaInstance) isProtectedUser(user *models.UserSearchHitDTO) bool {
	if user.IsAdmin {
		return true
	}
	if g.isProtectedLogin(user.Login) {
		return true
	}
	return strings.EqualFold(user.Email, protectedAdminEmail)
}

// deleteUnlistedUsers deletes every Grafana user that matches neither a user from the source
// (by login or email) nor a protected account. Users are global in Grafana, so a deletion
// removes them from all organizations.
func (g *GrafanaInstance) deleteUnlistedUsers(syncedTeams *Teams, dryRun bool) {
	usersLog := slog.With(slog.String("package", "grafana.unlisted.users"))

	if config.Instance.Features.DisableUserSync {
		usersLog.Info("usersync feature disabled, skipping unlisted user deletion")
		return
	}
	if config.Instance.Grafana.AuthType == configtypes.GrafanaAuthTypeToken {
		usersLog.Warn("can not delete unlisted users with token auth, skipping")
		return
	}

	var desired []string
	if syncedTeams != nil {
		for _, team := range *syncedTeams {
			if team.Users == nil {
				continue
			}
			for _, user := range *team.Users {
				desired = append(desired, user.Login, user.Email)
			}
		}
	}
	desired = slices.DeleteFunc(desired, func(s string) bool { return s == "" })
	// Deleting users is destructive, so never do it when the source produced no users at all.
	// Otherwise an empty or misconfigured source would remove every user.
	if len(desired) == 0 {
		usersLog.Warn("source produced no users, refusing to delete any Grafana users")
		return
	}

	allUsers, err := g.listAllUsers()
	if err != nil {
		usersLog.Error("could not list users", slog.Any("error", err))
		return
	}

	countDeleted := 0
	countPlanned := 0
	countSkipped := 0
	countFailed := 0

	for _, user := range allUsers {
		userLog := usersLog.With(
			slog.Group("user",
				slog.Int64("id", user.ID),
				slog.String("login", user.Login),
				slog.String("email", user.Email),
			),
		)

		if g.isProtectedUser(user) {
			countSkipped++
			userLog.Debug("skipping protected user")
			continue
		}
		if slices.ContainsFunc(desired, func(s string) bool {
			return strings.EqualFold(s, user.Login) || strings.EqualFold(s, user.Email)
		}) {
			continue
		}

		if dryRun {
			userLog.Info("dry run: would delete unlisted user")
			countPlanned++
			continue
		}

		if _, err := g.api.AdminUsers.AdminDeleteUser(user.ID); err != nil {
			userLog.Error("could not delete unlisted user", slog.Any("error", err))
			countFailed++
			continue
		}
		userLog.Info("deleted unlisted user")
		countDeleted++
	}

	usersLog.Info("finished processing unlisted users",
		slog.Group("users",
			slog.Int("deleted", countDeleted),
			slog.Int("planned", countPlanned),
			slog.Int("skippedProtected", countSkipped),
			slog.Int("failed", countFailed),
		),
	)
}
