//go:build !integration && !e2e

// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package grafana

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/skuethe/grafana-oss-team-sync/internal/config"
	"github.com/skuethe/grafana-oss-team-sync/internal/config/configtypes"
)

// unlistedFixture is the Grafana state served by newUnlistedServer.
type unlistedFixture struct {
	teams   []models.TeamDTO
	folders []map[string]any
	users   []models.UserSearchHitDTO
}

// recordedRequests collects every request the fake Grafana received, as "METHOD /path".
type recordedRequests struct {
	mu    sync.Mutex
	items []string
}

func (r *recordedRequests) add(req string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, req)
}

// deletes returns the recorded DELETE requests.
func (r *recordedRequests) deletes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, item := range r.items {
		if strings.HasPrefix(item, http.MethodDelete+" ") {
			out = append(out, item)
		}
	}
	return out
}

// newUnlistedServer serves the fixture and records all requests.
func newUnlistedServer(t *testing.T, fixture unlistedFixture) *recordedRequests {
	t.Helper()
	recorded := &recordedRequests{}

	newTestInstance(t, func(w http.ResponseWriter, r *http.Request) {
		recorded.add(r.Method + " " + r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/teams/search":
			writeJSON(t, w, map[string]any{"totalCount": len(fixture.teams), "teams": fixture.teams})
		case r.Method == http.MethodGet && r.URL.Path == "/api/folders":
			if r.URL.Query().Get("page") != "1" {
				writeJSON(t, w, []map[string]any{})
				return
			}
			writeJSON(t, w, fixture.folders)
		case r.Method == http.MethodGet && r.URL.Path == "/api/users":
			if r.URL.Query().Get("page") != "1" {
				writeJSON(t, w, []models.UserSearchHitDTO{})
				return
			}
			writeJSON(t, w, fixture.users)
		case r.Method == http.MethodDelete:
			writeJSON(t, w, map[string]string{"message": "deleted"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	})

	return recorded
}

// setUnlistedConfig installs a configuration for the duration of the test.
func setUnlistedConfig(t *testing.T, cfg *configtypes.Config) {
	t.Helper()
	previous := config.Instance
	config.Instance = cfg
	t.Cleanup(func() { config.Instance = previous })
}

func unlistedConfig(deleteUnlisted, dryRun bool) *configtypes.Config {
	return &configtypes.Config{
		Features: configtypes.Features{DeleteUnlisted: deleteUnlisted, DryRun: dryRun},
		Grafana:  configtypes.Grafana{AuthType: configtypes.GrafanaAuthTypeDefault},
	}
}

func TestDeleteUnlistedDisabledMakesNoRequests(t *testing.T) {
	setUnlistedConfig(t, unlistedConfig(false, false))
	recorded := newUnlistedServer(t, unlistedFixture{
		teams: []models.TeamDTO{{ID: int64Ptr(2), Name: strPtr("Old")}},
	})

	Instance.DeleteUnlisted(configtypes.Teams{{Name: "Dev"}}, &Teams{})

	if len(recorded.items) != 0 {
		t.Errorf("expected no requests when deleteUnlisted is disabled, got %v", recorded.items)
	}
}

func TestDeleteUnlistedTeams(t *testing.T) {
	tests := []struct {
		name        string
		dryRun      bool
		configured  configtypes.Teams
		wantDeletes []string
	}{
		{
			name:        "removes teams not in config",
			configured:  configtypes.Teams{{Name: "Dev", Orgs: map[int64]configtypes.OrgRole{1: configtypes.OrgRoleViewer}}},
			wantDeletes: []string{"DELETE /api/teams/2", "DELETE /api/teams/4"},
		},
		{
			name:        "dry run deletes nothing",
			dryRun:      true,
			configured:  configtypes.Teams{{Name: "Dev", Orgs: map[int64]configtypes.OrgRole{1: configtypes.OrgRoleViewer}}},
			wantDeletes: nil,
		},
		{
			name:        "no configured teams touches no organization",
			configured:  nil,
			wantDeletes: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setUnlistedConfig(t, unlistedConfig(true, test.dryRun))
			recorded := newUnlistedServer(t, unlistedFixture{
				teams: []models.TeamDTO{
					{ID: int64Ptr(1), Name: strPtr("Dev")},
					{ID: int64Ptr(2), Name: strPtr("Old")},
					{ID: int64Ptr(3), Name: strPtr("dev")},
					{ID: int64Ptr(4), Name: strPtr("Stale")},
				},
			})

			Instance.DeleteUnlisted(test.configured, &Teams{})

			if got := recorded.deletes(); !slices.Equal(got, test.wantDeletes) {
				t.Errorf("deletes: got %v, want %v", got, test.wantDeletes)
			}
		})
	}
}

func TestDeleteUnlistedFolders(t *testing.T) {
	tests := []struct {
		name        string
		dryRun      bool
		wantDeletes []string
	}{
		{name: "removes folders not in config", wantDeletes: []string{"DELETE /api/folders/stale-uid"}},
		{name: "dry run deletes nothing", dryRun: true, wantDeletes: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := unlistedConfig(true, test.dryRun)
			cfg.Folders = configtypes.Folders{"keep": {Title: "Keep"}}
			setUnlistedConfig(t, cfg)
			recorded := newUnlistedServer(t, unlistedFixture{
				folders: []map[string]any{
					{"uid": "keep-uid", "title": "Keep"},
					{"uid": "stale-uid", "title": "Stale"},
				},
			})

			Instance.deleteUnlistedFolders(test.dryRun)

			if got := recorded.deletes(); !slices.Equal(got, test.wantDeletes) {
				t.Errorf("deletes: got %v, want %v", got, test.wantDeletes)
			}
		})
	}
}

func TestDeleteUnlistedUsers(t *testing.T) {
	syncedTeams := &Teams{
		{
			Parameter: &TeamParameter{Name: strPtr("Dev")},
			Users:     &Users{{Login: "alice", Email: "alice@example.com"}},
		},
	}
	fixture := unlistedFixture{
		users: []models.UserSearchHitDTO{
			{ID: 1, Login: "admin", Email: "admin@localhost"},
			{ID: 2, Login: "alice", Email: "alice@example.com"},
			{ID: 3, Login: "bob", Email: "bob@example.com"},
			{ID: 4, Login: "sync", Email: "sync@example.com"},
			{ID: 5, Login: "root", Email: "root@example.com", IsAdmin: true},
		},
	}

	t.Run("removes users not in source and keeps protected accounts", func(t *testing.T) {
		setUnlistedConfig(t, unlistedConfig(true, false))
		recorded := newUnlistedServer(t, fixture)
		Instance.syncUserLogin = "sync"

		Instance.deleteUnlistedUsers(syncedTeams, false)

		want := []string{"DELETE /api/admin/users/3"}
		if got := recorded.deletes(); !slices.Equal(got, want) {
			t.Errorf("deletes: got %v, want %v", got, want)
		}
	})

	t.Run("dry run deletes nothing", func(t *testing.T) {
		setUnlistedConfig(t, unlistedConfig(true, true))
		recorded := newUnlistedServer(t, fixture)
		Instance.syncUserLogin = "sync"

		Instance.deleteUnlistedUsers(syncedTeams, true)

		if got := recorded.deletes(); len(got) != 0 {
			t.Errorf("expected no deletes in dry run, got %v", got)
		}
	})

	t.Run("empty source refuses to delete any user", func(t *testing.T) {
		setUnlistedConfig(t, unlistedConfig(true, false))
		recorded := newUnlistedServer(t, fixture)

		Instance.deleteUnlistedUsers(&Teams{}, false)

		if len(recorded.items) != 0 {
			t.Errorf("expected no requests for an empty source, got %v", recorded.items)
		}
	})

	t.Run("user sync disabled skips user deletion", func(t *testing.T) {
		cfg := unlistedConfig(true, false)
		cfg.Features.DisableUserSync = true
		setUnlistedConfig(t, cfg)
		recorded := newUnlistedServer(t, fixture)

		Instance.deleteUnlistedUsers(syncedTeams, false)

		if len(recorded.items) != 0 {
			t.Errorf("expected no requests with user sync disabled, got %v", recorded.items)
		}
	})

	t.Run("token auth skips user deletion", func(t *testing.T) {
		cfg := unlistedConfig(true, false)
		cfg.Grafana.AuthType = configtypes.GrafanaAuthTypeToken
		setUnlistedConfig(t, cfg)
		recorded := newUnlistedServer(t, fixture)

		Instance.deleteUnlistedUsers(syncedTeams, false)

		if len(recorded.items) != 0 {
			t.Errorf("expected no requests with token auth, got %v", recorded.items)
		}
	})
}
