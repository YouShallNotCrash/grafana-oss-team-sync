//go:build !integration && !e2e

// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package grafana

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
)

// newTestInstance points the shared Grafana instance at a fake API served by handler.
func newTestInstance(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	api := client.NewHTTPClientWithConfig(strfmt.Default, &client.TransportConfig{
		Host:     strings.TrimPrefix(srv.URL, "http://"),
		BasePath: "/api",
		Schemes:  []string{"http"},
	})
	Instance = &GrafanaInstance{api: api, defaultOrgID: 1, currentOrgID: 1}
	t.Cleanup(func() { Instance = nil })
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("could not encode response: %v", err)
	}
}

func TestFolderSearchFollowsPagination(t *testing.T) {
	// Three pages: 1000, 1000 and 500 folders. The target folder only exists on the last page.
	pageSizes := map[int64]int{1: 1000, 2: 1000, 3: 500}
	var mu sync.Mutex
	var requestedPages []int64

	newTestInstance(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/folders" {
			http.NotFound(w, r)
			return
		}
		page, err := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64)
		if err != nil {
			http.Error(w, "missing page", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requestedPages = append(requestedPages, page)
		mu.Unlock()

		size, ok := pageSizes[page]
		if !ok {
			writeJSON(t, w, []models.FolderSearchHit{})
			return
		}
		hits := make([]models.FolderSearchHit, 0, size)
		for i := range size {
			title := fmt.Sprintf("folder-p%d-%d", page, i)
			if page == 3 && i == 250 {
				title = "target"
			}
			hits = append(hits, models.FolderSearchHit{UID: title, Title: title})
		}
		writeJSON(t, w, hits)
	})

	found, err := (&Folder{CreateFolderCommand: models.CreateFolderCommand{Title: "target"}}).searchFolder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil || found.Title != "target" {
		t.Fatalf("expected to find folder %q on the last page, got %+v", "target", found)
	}
	if fmt.Sprint(requestedPages) != "[1 2 3]" {
		t.Errorf("expected pages [1 2 3] to be requested, got %v", requestedPages)
	}
}

func TestFolderSearchMissingFolderStopsAtLastPage(t *testing.T) {
	var mu sync.Mutex
	var requestedPages []int64

	newTestInstance(t, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64)
		mu.Lock()
		requestedPages = append(requestedPages, page)
		mu.Unlock()
		if page == 1 {
			hits := make([]models.FolderSearchHit, 0, 1000)
			for i := range 1000 {
				hits = append(hits, models.FolderSearchHit{UID: strconv.Itoa(i), Title: strconv.Itoa(i)})
			}
			writeJSON(t, w, hits)
			return
		}
		writeJSON(t, w, []models.FolderSearchHit{{UID: "x", Title: "other"}})
	})

	found, err := (&Folder{CreateFolderCommand: models.CreateFolderCommand{Title: "missing"}}).searchFolder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found != nil {
		t.Fatalf("expected no folder, got %+v", found)
	}
	if fmt.Sprint(requestedPages) != "[1 2]" {
		t.Errorf("expected pages [1 2] to be requested, got %v", requestedPages)
	}
}

func TestFindTeamRequiresExactName(t *testing.T) {
	tests := []struct {
		name       string
		searchHits []models.TeamDTO
		expectedID *int64
		exists     bool
	}{
		{
			name:       "partial match only is not the team",
			searchHits: []models.TeamDTO{{ID: int64Ptr(1), Name: strPtr("Dev2")}},
			expectedID: nil,
			exists:     false,
		},
		{
			name:       "exact match is picked among partial matches",
			searchHits: []models.TeamDTO{{ID: int64Ptr(1), Name: strPtr("Dev2")}, {ID: int64Ptr(2), Name: strPtr("Dev")}},
			expectedID: int64Ptr(2),
			exists:     true,
		},
		{
			name:       "no results",
			searchHits: []models.TeamDTO{},
			expectedID: nil,
			exists:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newTestInstance(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/teams/search" {
					http.NotFound(w, r)
					return
				}
				writeJSON(t, w, map[string]any{"totalCount": len(test.searchHits), "teams": test.searchHits})
			})

			team := &Team{Parameter: &TeamParameter{Name: strPtr("Dev")}}

			exists, err := team.doesTeamExist()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exists != test.exists {
				t.Errorf("doesTeamExist: got %v, want %v", exists, test.exists)
			}

			id, err := team.getTeamID()
			if test.expectedID == nil {
				if !errors.Is(err, ErrTeamNotFound) {
					t.Errorf("getTeamID: expected ErrTeamNotFound, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *id != *test.expectedID {
				t.Errorf("getTeamID: got %d, want %d", *id, *test.expectedID)
			}
		})
	}
}

// newPagedTeamsServer serves teams/search in pages of the given sizes and records the requested pages.
// Each team is named "team-p<page>-<index>", except the one at target, which is named "target".
func newPagedTeamsServer(t *testing.T, pageSizes map[int64]int, target string, targetPage int64, targetIndex int) *[]int64 {
	t.Helper()
	var mu sync.Mutex
	var requestedPages []int64

	newTestInstance(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/teams/search" {
			http.NotFound(w, r)
			return
		}
		page, err := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64)
		if err != nil {
			http.Error(w, "missing page", http.StatusBadRequest)
			return
		}
		if perpage := r.URL.Query().Get("perpage"); perpage != strconv.Itoa(int(teamPageSize)) {
			t.Errorf("expected perpage %d, got %q", teamPageSize, perpage)
		}
		mu.Lock()
		requestedPages = append(requestedPages, page)
		mu.Unlock()

		size, ok := pageSizes[page]
		if !ok {
			writeJSON(t, w, map[string]any{"totalCount": 0, "teams": []models.TeamDTO{}})
			return
		}
		teamsOnPage := make([]models.TeamDTO, 0, size)
		for i := range size {
			name := fmt.Sprintf("team-p%d-%d", page, i)
			if page == targetPage && i == targetIndex {
				name = target
			}
			teamsOnPage = append(teamsOnPage, models.TeamDTO{ID: int64Ptr(page*10000 + int64(i)), Name: strPtr(name)})
		}
		writeJSON(t, w, map[string]any{"totalCount": 2500, "teams": teamsOnPage})
	})

	return &requestedPages
}

func TestFindTeamFollowsPagination(t *testing.T) {
	// Three pages: 1000, 1000 and 500 teams. The target team only exists on the last page.
	requestedPages := newPagedTeamsServer(t, map[int64]int{1: 1000, 2: 1000, 3: 500}, "target", 3, 250)

	id, err := (&Team{Parameter: &TeamParameter{Name: strPtr("target")}}).getTeamID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *id != 3*10000+250 {
		t.Errorf("getTeamID: got %d, want %d", *id, 3*10000+250)
	}
	if fmt.Sprint(*requestedPages) != "[1 2 3]" {
		t.Errorf("expected pages [1 2 3] to be requested, got %v", *requestedPages)
	}
}

func TestFindTeamMissingTeamStopsAtLastPage(t *testing.T) {
	// Target does not exist. Two full pages followed by a short page must end the search.
	requestedPages := newPagedTeamsServer(t, map[int64]int{1: 1000, 2: 1000, 3: 500}, "", 0, -1)

	team := &Team{Parameter: &TeamParameter{Name: strPtr("target")}}
	exists, err := team.doesTeamExist()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("doesTeamExist: expected false for a missing team")
	}
	if fmt.Sprint(*requestedPages) != "[1 2 3]" {
		t.Errorf("expected pages [1 2 3] to be requested, got %v", *requestedPages)
	}
}

func int64Ptr(i int64) *int64 { return &i }
func strPtr(s string) *string { return &s }
