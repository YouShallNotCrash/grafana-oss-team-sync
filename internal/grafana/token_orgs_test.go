//go:build !integration && !e2e

// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package grafana

import (
	"errors"
	"testing"

	"github.com/skuethe/grafana-oss-team-sync/internal/config/configtypes"
)

func TestValidateTokenOrgs(t *testing.T) {
	const tokenOrg = int64(1)

	tests := []struct {
		name    string
		teams   configtypes.Teams
		folders configtypes.Folders
		wantErr error
	}{
		{
			name:  "team in token org is allowed",
			teams: configtypes.Teams{{Name: "a", Orgs: map[int64]configtypes.OrgRole{1: configtypes.OrgRoleViewer}}},
		},
		{
			name:    "team in other org is rejected",
			teams:   configtypes.Teams{{Name: "a", Orgs: map[int64]configtypes.OrgRole{2: configtypes.OrgRoleViewer}}},
			wantErr: configtypes.ErrMultiOrgRequiresBasicAuth,
		},
		{
			name: "team with one org outside token org is rejected",
			teams: configtypes.Teams{{Name: "a", Orgs: map[int64]configtypes.OrgRole{
				1: configtypes.OrgRoleViewer,
				2: configtypes.OrgRoleEditor,
			}}},
			wantErr: configtypes.ErrMultiOrgRequiresBasicAuth,
		},
		{
			name:    "folder in default org 0 is allowed",
			folders: configtypes.Folders{"f1": {}},
		},
		{
			name:    "folder in token org is allowed",
			folders: configtypes.Folders{"f1": {OrgID: 1}},
		},
		{
			name:    "folder in other org is rejected",
			folders: configtypes.Folders{"f1": {OrgID: 2}},
			wantErr: configtypes.ErrMultiOrgRequiresBasicAuth,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateTokenOrgs(tokenOrg, test.teams, test.folders)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
		})
	}
}
