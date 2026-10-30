//go:build !integration && !e2e

// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"reflect"
	"testing"

	"github.com/skuethe/grafana-oss-team-sync/internal/config/configtypes"
)

func TestSplitTeamNames(t *testing.T) {
	tests := map[string][]string{
		"":                  nil,
		" , ":               nil,
		"group-a":           {"group-a"},
		"group-a, group-b":  {"group-a", "group-b"},
		"group-a,,group-b,": {"group-a", "group-b"},
	}
	for in, want := range tests {
		if got := splitTeamNames(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitTeamNames(%q): got %v, want %v", in, got, want)
		}
	}
}

func TestTeamStringDecodeHookRejectsStrings(t *testing.T) {
	teamType := reflect.TypeOf(configtypes.Team{})
	_, err := teamStringDecodeHook(reflect.TypeOf(""), teamType, "group-a")
	if !errors.Is(err, configtypes.ErrTeamWithoutOrgs) {
		t.Errorf("expected ErrTeamWithoutOrgs for a plain team string, got %v", err)
	}
}
