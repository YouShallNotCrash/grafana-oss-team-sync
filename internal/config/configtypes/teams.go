// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package configtypes

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Team describes a single group/team to sync and the Grafana organizations it should be synced into.
//
// In YAML, a team is an object with a "name" and an "orgs" map, which maps each organization ID
// to the organization role that members of the group receive in that organization.
// At least one organization is required:
//
//	teams:
//	  - name: myTeamInOrg2
//	    orgs:
//	      2: Viewer
//	  - name: myTeamWithRoles
//	    orgs:
//	      2: Editor
//	      3: Admin
type Team struct {
	Name string            `yaml:"name"`
	Orgs map[int64]OrgRole `yaml:"orgs"`
}

// TargetOrgs returns the Grafana organizations this entry syncs into, in ascending order.
func (t Team) TargetOrgs() []int64 {
	return slices.Sorted(maps.Keys(t.Orgs))
}

// RoleFor returns the organization role configured for orgID, or an empty role if none is configured.
func (t Team) RoleFor(orgID int64) OrgRole {
	return t.Orgs[orgID]
}

type Teams []Team

// OrgRole is the Grafana organization role granted to the members of a group.
type OrgRole string

const (
	OrgRoleViewer OrgRole = "Viewer"
	OrgRoleEditor OrgRole = "Editor"
	OrgRoleAdmin  OrgRole = "Admin"
)

var (
	ErrInvalidOrgRole   = errors.New("invalid organization role defined")
	ErrInvalidTeamOrgID = errors.New("invalid organization ID defined in teams orgs, IDs must be greater than 0")
	ErrTeamWithoutOrgs  = errors.New("a team must define at least one organization with a role in orgs")
)

// Rank orders the roles by privilege. An unknown or empty role ranks 0.
func (r OrgRole) Rank() int {
	switch r {
	case OrgRoleViewer:
		return 1
	case OrgRoleEditor:
		return 2
	case OrgRoleAdmin:
		return 3
	}
	return 0
}

// ParseOrgRole returns the canonical role for the given name (case-insensitive).
func ParseOrgRole(in string) (OrgRole, error) {
	for _, role := range []OrgRole{OrgRoleViewer, OrgRoleEditor, OrgRoleAdmin} {
		if strings.EqualFold(in, string(role)) {
			return role, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidOrgRole, in)
}

const (
	TeamsDefault   string = ""
	TeamsFlagHelp  string = "the comma-separated list of teams you want to sync"
	TeamsParameter string = "teams"
)

// ValidateTeamOrgs checks that every team defines at least one organization and that each
// organization ID and role is valid. It also normalizes the role names.
func (c *Config) ValidateTeamOrgs() error {
	for i, team := range c.Teams {
		if len(team.Orgs) == 0 {
			return fmt.Errorf("%w (team %q)", ErrTeamWithoutOrgs, team.Name)
		}
		for orgID, role := range team.Orgs {
			if orgID <= 0 {
				return fmt.Errorf("%w (team %q, orgId %d)", ErrInvalidTeamOrgID, team.Name, orgID)
			}
			canonical, err := ParseOrgRole(string(role))
			if err != nil {
				return fmt.Errorf("%w (team %q, orgId %d)", err, team.Name, orgID)
			}
			c.Teams[i].Orgs[orgID] = canonical
		}
	}
	return nil
}

// Names returns the plain list of team display names, e.g. to query a source plugin.
func (t Teams) Names() []string {
	names := make([]string, len(t))
	for i, team := range t {
		names[i] = team.Name
	}
	return names
}

// Find returns the team matching the given name (case-insensitive), if any.
func (t Teams) Find(name string) (Team, bool) {
	for _, team := range t {
		if strings.EqualFold(team.Name, name) {
			return team, true
		}
	}
	return Team{}, false
}

// FindAll returns every configured team entry matching the given name (case-insensitive).
// The same source group/team name can be configured multiple times to sync into several
// Grafana organizations, e.g. one plain entry plus one or more "orgId" entries.
func (t Teams) FindAll(name string) Teams {
	var result Teams
	for _, team := range t {
		if strings.EqualFold(team.Name, name) {
			result = append(result, team)
		}
	}
	return result
}

// Remove returns a copy of the teams list with the team matching the given name (case-insensitive) removed.
func (t Teams) Remove(name string) Teams {
	result := make(Teams, 0, len(t))
	for _, team := range t {
		if !strings.EqualFold(team.Name, name) {
			result = append(result, team)
		}
	}
	return result
}
