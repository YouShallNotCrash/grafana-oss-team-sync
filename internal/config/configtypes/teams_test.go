//go:build !integration && !e2e

// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package configtypes

import (
	"errors"
	"fmt"
	"testing"
)

func TestParseOrgRole(t *testing.T) {
	tests := map[string]OrgRole{
		"Viewer": OrgRoleViewer,
		"editor": OrgRoleEditor,
		"ADMIN":  OrgRoleAdmin,
	}
	for in, want := range tests {
		got, err := ParseOrgRole(in)
		if err != nil {
			t.Fatalf("ParseOrgRole(%q): unexpected error: %v", in, err)
		}
		if got != want {
			t.Errorf("ParseOrgRole(%q): got %q, want %q", in, got, want)
		}
	}

	if _, err := ParseOrgRole("Owner"); !errors.Is(err, ErrInvalidOrgRole) {
		t.Errorf("ParseOrgRole(Owner): expected ErrInvalidOrgRole, got %v", err)
	}
}

func TestOrgRoleRank(t *testing.T) {
	if !(OrgRole("").Rank() < OrgRoleViewer.Rank() &&
		OrgRoleViewer.Rank() < OrgRoleEditor.Rank() &&
		OrgRoleEditor.Rank() < OrgRoleAdmin.Rank()) {
		t.Errorf("unexpected rank order: none=%d viewer=%d editor=%d admin=%d",
			OrgRole("").Rank(), OrgRoleViewer.Rank(), OrgRoleEditor.Rank(), OrgRoleAdmin.Rank())
	}
}

func TestTeamTargetOrgs(t *testing.T) {
	tests := []struct {
		name string
		team Team
		want []int64
	}{
		{name: "no orgs returns empty", team: Team{Name: "a"}, want: []int64{}},
		{
			name: "orgs are sorted ascending",
			team: Team{Name: "a", Orgs: map[int64]OrgRole{3: OrgRoleAdmin, 2: OrgRoleEditor}},
			want: []int64{2, 3},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.team.TargetOrgs()
			if fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Errorf("TargetOrgs: got %v, want %v", got, test.want)
			}
		})
	}
}

func TestTeamRoleFor(t *testing.T) {
	team := Team{Name: "a", Orgs: map[int64]OrgRole{2: OrgRoleEditor}}
	if got := team.RoleFor(2); got != OrgRoleEditor {
		t.Errorf("RoleFor(2): got %q, want %q", got, OrgRoleEditor)
	}
	if got := team.RoleFor(3); got != "" {
		t.Errorf("RoleFor(3): got %q, want empty", got)
	}
	if got := (Team{Name: "plain"}).RoleFor(0); got != "" {
		t.Errorf("RoleFor on plain entry: got %q, want empty", got)
	}
}

func TestValidateTeamOrgs(t *testing.T) {
	tests := []struct {
		name    string
		teams   Teams
		wantErr error
		// wantRoles are the canonical roles expected after validation, keyed by org ID.
		wantRoles map[int64]OrgRole
	}{
		{
			name:  "team with orgs is valid",
			teams: Teams{{Name: "a", Orgs: map[int64]OrgRole{2: OrgRoleViewer}}},
		},
		{
			name:    "team without orgs is rejected",
			teams:   Teams{{Name: "a"}},
			wantErr: ErrTeamWithoutOrgs,
		},
		{
			name:      "roles are canonicalized",
			teams:     Teams{{Name: "a", Orgs: map[int64]OrgRole{2: "editor", 3: "ADMIN"}}},
			wantRoles: map[int64]OrgRole{2: OrgRoleEditor, 3: OrgRoleAdmin},
		},
		{
			name:    "org ID must be greater than zero",
			teams:   Teams{{Name: "a", Orgs: map[int64]OrgRole{0: OrgRoleViewer}}},
			wantErr: ErrInvalidTeamOrgID,
		},
		{
			name:    "negative org ID is rejected",
			teams:   Teams{{Name: "a", Orgs: map[int64]OrgRole{-1: OrgRoleViewer}}},
			wantErr: ErrInvalidTeamOrgID,
		},
		{
			name:    "unknown role is rejected",
			teams:   Teams{{Name: "a", Orgs: map[int64]OrgRole{2: "Owner"}}},
			wantErr: ErrInvalidOrgRole,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Teams: test.teams}
			err := cfg.ValidateTeamOrgs()
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("expected %v, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for orgID, want := range test.wantRoles {
				if got := cfg.Teams[0].Orgs[orgID]; got != want {
					t.Errorf("role for org %d: got %q, want %q", orgID, got, want)
				}
			}
		})
	}
}
