// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package grafana

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/orgs"
	"github.com/grafana/grafana-openapi-client-go/client/teams"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/skuethe/grafana-oss-team-sync/internal/config"
	"github.com/skuethe/grafana-oss-team-sync/internal/config/configtypes"
)

var ErrTeamNotFound = errors.New("team not found in organization")

type TeamParameter models.CreateTeamCommand

type Team struct {
	Parameter *TeamParameter
	Users     *Users
	// OrgID is the Grafana organization to sync this team into.
	// 0 (unset) uses whichever org the configured Grafana credentials default to.
	OrgID int64
	// Role is the organization role members of this team receive in OrgID.
	// Empty means no role is configured: new members get Viewer and existing roles are left unchanged.
	Role configtypes.OrgRole
}

type Teams []Team

// orgUserKey identifies a user, by lowercase email, within one organization.
type orgUserKey struct {
	orgID int64
	email string
}

// desiredOrgRoles holds the highest configured organization role for each user in each organization.
type desiredOrgRoles map[orgUserKey]configtypes.OrgRole

// desiredOrgRoles collects the configured organization roles of all teams. A user who is a member of
// several groups that configure a role in the same organization gets the highest of those roles.
func (t Teams) desiredOrgRoles() desiredOrgRoles {
	roles := desiredOrgRoles{}
	for _, team := range t {
		if team.Role == "" || team.Users == nil {
			continue
		}
		for _, user := range *team.Users {
			if user.Email == "" {
				continue
			}
			key := orgUserKey{orgID: team.OrgID, email: strings.ToLower(user.Email)}
			if roles[key].Rank() < team.Role.Rank() {
				roles[key] = team.Role
			}
		}
	}
	return roles
}

// Contains reports whether the given team name was synced into orgID as part of this list.
func (t Teams) Contains(name string, orgID int64) bool {
	for _, team := range t {
		if team.OrgID == orgID && team.Parameter != nil && team.Parameter.Name != nil && strings.EqualFold(*team.Parameter.Name, name) {
			return true
		}
	}
	return false
}

// api switches the shared Grafana client into this team's organization and returns it.
func (t *Team) api() (*client.GrafanaHTTPAPI, error) {
	if err := Instance.EnsureOrgContext(t.OrgID); err != nil {
		return nil, err
	}
	return Instance.api, nil
}

// ensureOrgMembership makes sure the given user/login is a member of this team's organization,
// which is required before it can be added as a member of a team in that organization.
// A new member gets desiredRole, or Viewer when no role is configured. An existing member's role is
// only changed when a role is configured and differs; protected accounts are never changed.
func (t *Team) ensureOrgMembership(loginOrEmail string, desiredRole configtypes.OrgRole) error {
	if t.OrgID == 0 {
		return nil
	}

	orgUsers, err := Instance.api.Orgs.GetOrgUsers(t.OrgID)
	if err != nil {
		return err
	}
	for _, orgUser := range orgUsers.Payload {
		if !strings.EqualFold(orgUser.Login, loginOrEmail) && !strings.EqualFold(orgUser.Email, loginOrEmail) {
			continue
		}
		if desiredRole == "" || strings.EqualFold(orgUser.Role, string(desiredRole)) || Instance.isProtectedLogin(orgUser.Login) {
			return nil
		}
		slog.Info("updating organization role of existing member",
			slog.String("user", loginOrEmail),
			slog.Int64("orgId", t.OrgID),
			slog.String("from", orgUser.Role),
			slog.String("to", string(desiredRole)),
		)
		_, err := Instance.api.Orgs.UpdateOrgUser(&orgs.UpdateOrgUserParams{
			OrgID:  t.OrgID,
			UserID: orgUser.UserID,
			Body:   &models.UpdateOrgUserCommand{Role: string(desiredRole)},
		})
		return err
	}

	role := string(configtypes.OrgRoleViewer)
	if desiredRole != "" {
		role = string(desiredRole)
	}
	_, err = Instance.api.Orgs.AddOrgUser(t.OrgID, &models.AddOrgUserCommand{
		LoginOrEmail: loginOrEmail,
		Role:         role,
	})
	return err
}

// teamPageSize is the number of teams requested per page when searching by name.
const teamPageSize int64 = 1000

// findTeam returns the team in the current organization whose name exactly matches the configured name.
// Returns nil if no such team exists.
func (t *Team) findTeam() (*models.TeamDTO, error) {
	api, err := t.api()
	if err != nil {
		return nil, err
	}
	// Grafana pages team results (1-based); a name filter can match more teams than fit on one page
	for page := int64(1); ; page++ {
		limit := teamPageSize
		pageNumber := page
		result, err := api.Teams.SearchTeams(&teams.SearchTeamsParams{
			Name:    t.Parameter.Name,
			Page:    &pageNumber,
			Perpage: &limit,
		})
		if err != nil {
			return nil, err
		}
		index := slices.IndexFunc(result.Payload.Teams, func(s *models.TeamDTO) bool {
			return s.Name != nil && t.Parameter.Name != nil && *s.Name == *t.Parameter.Name
		})
		if index >= 0 {
			return result.Payload.Teams[index], nil
		}
		if int64(len(result.Payload.Teams)) < teamPageSize {
			return nil, nil
		}
	}
}

func (t *Team) doesTeamExist() (bool, error) {
	team, err := t.findTeam()
	if err != nil {
		return false, err
	}
	return team != nil, nil
}

func (t *Team) getTeamID() (*int64, error) {
	team, err := t.findTeam()
	if err != nil {
		return nil, err
	}
	if team == nil {
		return nil, ErrTeamNotFound
	}
	return team.ID, nil
}

func (t *Team) createTeam() error {
	api, err := t.api()
	if err != nil {
		return err
	}
	_, err = api.Teams.CreateTeam(&models.CreateTeamCommand{
		Name:  t.Parameter.Name,
		Email: t.Parameter.Email,
	})
	if err != nil {
		return err
	}
	return nil
}

func (t *Team) addUsersToTeam(roles desiredOrgRoles) (*[]string, error) {
	teamID, tErr := t.getTeamID()
	if tErr != nil {
		return nil, tErr
	}

	adminMemberList := &[]string{}
	teamMemberList := &[]string{}

	if config.Instance.Features.AddLocalAdminToTeams {
		if err := t.ensureOrgMembership("admin@localhost", ""); err != nil {
			slog.Warn("could not add local admin to organization",
				slog.Int64("orgId", t.OrgID),
				slog.Any("error", err),
			)
		} else {
			*adminMemberList = append(*adminMemberList, "admin@localhost")
		}
	}

	for _, user := range *t.Users {
		if user.Email == "" {
			slog.Warn("skipping user with no mail",
				slog.Group("user",
					slog.String("login", user.Login),
					slog.String("name", user.Name),
				),
			)
			continue
		}
		role := roles[orgUserKey{orgID: t.OrgID, email: strings.ToLower(user.Email)}]
		if err := t.ensureOrgMembership(user.Email, role); err != nil {
			slog.Warn("could not add user to organization, skipping team membership",
				slog.String("user", user.Email),
				slog.Int64("orgId", t.OrgID),
				slog.Any("error", err),
			)
			continue
		}
		*teamMemberList = append(*teamMemberList, user.Email)
	}

	api, err := t.api()
	if err != nil {
		return nil, err
	}
	if _, err := api.Teams.SetTeamMemberships(strconv.FormatInt(*teamID, 10), &models.SetTeamMembershipsCommand{
		Admins:  *adminMemberList,
		Members: *teamMemberList,
	}); err != nil {
		return nil, fmt.Errorf("%w. Inputs: %v; %v", err, *adminMemberList, *teamMemberList)
	}

	return teamMemberList, nil
}

func (t *Teams) ProcessTeams() {
	teamsLog := slog.With(slog.String("package", "grafana.teams"))

	if len(*t) == 0 {
		teamsLog.Info("no teams to process, skipping")
	} else {
		teamsLog.Info("processing teams")

		countSkipped := 0
		countCreated := 0
		roles := t.desiredOrgRoles()

		for _, team := range *t {

			teamLog := slog.With(
				slog.Group("team",
					slog.String("name", *team.Parameter.Name),
					slog.Int64("orgId", team.OrgID),
				),
			)

			exists, err := team.doesTeamExist()
			if err != nil {
				teamLog.Error("could not search for team",
					slog.Any("error", err),
				)
			} else {
				if exists {
					countSkipped++
					teamLog.Debug("skipping already existing team")
				} else {
					err := team.createTeam()
					if err != nil {
						teamLog.Error("could not create team",
							slog.Any("error", err),
						)
						continue
					} else {
						teamLog.Info("created team")
						countCreated++
					}
				}
				// Add users to team, if userSync feature is enabled
				if !config.Instance.Features.DisableUserSync {
					teamLog.Info("processing team members")

					if userList, err := team.addUsersToTeam(roles); err != nil {
						teamLog.Error("could not add users to team",
							slog.Any("error", err),
						)
					} else {
						teamLog.Debug("added users to team",
							slog.Any("list", *userList),
						)
					}

					teamLog.Info("finished processing team members")
				}
			}
		}

		teamsLog.Info("finished processing teams",
			slog.Group("teams",
				slog.Int("created", countCreated),
				slog.Int("existing", countSkipped),
			),
		)
	}
}
