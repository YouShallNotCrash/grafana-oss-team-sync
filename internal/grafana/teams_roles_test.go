//go:build !integration && !e2e

// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package grafana

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/skuethe/grafana-oss-team-sync/internal/config/configtypes"
)

// orgCall records one request made against the organization users API.
type orgCall struct {
	Method string
	Path   string
	Role   string
}

// newOrgUsersServer serves GET /orgs/{id}/users from existing and records every add/update call.
func newOrgUsersServer(t *testing.T, existing []models.OrgUserDTO) *[]orgCall {
	t.Helper()
	var mu sync.Mutex
	var calls []orgCall

	newTestInstance(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/orgs/2/users":
			writeJSON(t, w, existing)
		case r.Method == http.MethodPost && r.URL.Path == "/api/orgs/2/users",
			r.Method == http.MethodPatch && r.URL.Path == "/api/orgs/2/users/7":
			body, _ := io.ReadAll(r.Body)
			var cmd struct {
				Role string `json:"role"`
			}
			_ = json.Unmarshal(body, &cmd)
			mu.Lock()
			calls = append(calls, orgCall{Method: r.Method, Path: r.URL.Path, Role: cmd.Role})
			mu.Unlock()
			writeJSON(t, w, map[string]any{"message": "ok"})
		default:
			http.NotFound(w, r)
		}
	})

	return &calls
}

func TestEnsureOrgMembershipRoles(t *testing.T) {
	member := func(login, role string) models.OrgUserDTO {
		return models.OrgUserDTO{UserID: 7, Login: login, Email: login + "@example.com", Role: role}
	}

	tests := []struct {
		name     string
		login    string
		existing []models.OrgUserDTO
		desired  configtypes.OrgRole
		// want lists the expected calls: POST for adds, PATCH for role updates.
		want []orgCall
	}{
		{
			name:    "new member gets the configured role",
			login:   "new@example.com",
			desired: configtypes.OrgRoleEditor,
			want:    []orgCall{{Method: http.MethodPost, Path: "/api/orgs/2/users", Role: "Editor"}},
		},
		{
			name:    "new member without a configured role gets Viewer",
			login:   "new@example.com",
			desired: "",
			want:    []orgCall{{Method: http.MethodPost, Path: "/api/orgs/2/users", Role: "Viewer"}},
		},
		{
			name:     "existing member is promoted to the configured role",
			login:    "a@example.com",
			existing: []models.OrgUserDTO{member("a", "Viewer")},
			desired:  configtypes.OrgRoleAdmin,
			want:     []orgCall{{Method: http.MethodPatch, Path: "/api/orgs/2/users/7", Role: "Admin"}},
		},
		{
			name:     "existing member with the configured role is left alone",
			login:    "a@example.com",
			existing: []models.OrgUserDTO{member("a", "Editor")},
			desired:  configtypes.OrgRoleEditor,
			want:     nil,
		},
		{
			name:     "implicit entry never downgrades an existing member",
			login:    "a@example.com",
			existing: []models.OrgUserDTO{member("a", "Admin")},
			desired:  "",
			want:     nil,
		},
		{
			name:     "protected sync user is never changed",
			login:    "sync",
			existing: []models.OrgUserDTO{member("sync", "Viewer")},
			desired:  configtypes.OrgRoleEditor,
			want:     nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := newOrgUsersServer(t, test.existing)
			Instance.syncUserLogin = "sync"

			team := &Team{OrgID: 2}
			if err := team.ensureOrgMembership(test.login, test.desired); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(*calls) != len(test.want) {
				t.Fatalf("expected calls %+v, got %+v", test.want, *calls)
			}
			for i := range test.want {
				if (*calls)[i] != test.want[i] {
					t.Errorf("call %d: got %+v, want %+v", i, (*calls)[i], test.want[i])
				}
			}
		})
	}
}

func TestEnsureOrgMembershipDefaultOrgIsNotTouched(t *testing.T) {
	calls := newOrgUsersServer(t, nil)

	team := &Team{OrgID: 0}
	if err := team.ensureOrgMembership("a@example.com", configtypes.OrgRoleAdmin); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no organization API calls for the default org, got %+v", *calls)
	}
}

func TestDesiredOrgRolesHighestRoleWins(t *testing.T) {
	teams := Teams{
		{OrgID: 2, Role: configtypes.OrgRoleViewer, Users: &Users{{Email: "a@example.com"}, {Email: "b@example.com"}}},
		{OrgID: 2, Role: configtypes.OrgRoleAdmin, Users: &Users{{Email: "A@example.com"}}},
		{OrgID: 3, Role: configtypes.OrgRoleEditor, Users: &Users{{Email: "a@example.com"}}},
		// Implicit entry: contributes no role.
		{OrgID: 2, Users: &Users{{Email: "b@example.com"}}},
		// Users without an email cannot be matched and are skipped.
		{OrgID: 2, Role: configtypes.OrgRoleEditor, Users: &Users{{Login: "nomail"}}},
	}

	got := teams.desiredOrgRoles()
	want := desiredOrgRoles{
		{orgID: 2, email: "a@example.com"}: configtypes.OrgRoleAdmin,
		{orgID: 2, email: "b@example.com"}: configtypes.OrgRoleViewer,
		{orgID: 3, email: "a@example.com"}: configtypes.OrgRoleEditor,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries, got %d: %v", len(want), len(got), got)
	}
	for key, role := range want {
		if got[key] != role {
			t.Errorf("%+v: got %q, want %q", key, got[key], role)
		}
	}
}
