//go:build !integration && !e2e

// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package entraid

import (
	"testing"

	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/skuethe/grafana-oss-team-sync/internal/grafana"
)

func newUser(displayName, upn, mail *string) models.Userable {
	user := models.NewUser()
	user.SetDisplayName(displayName)
	user.SetUserPrincipalName(upn)
	user.SetMail(mail)
	return user
}

func TestProcessUserResultHandlesMissingFields(t *testing.T) {
	name := "Jane Doe"
	upn := "jane.doe@example.com"
	mail := "Jane.Doe@Example.com"

	tests := []struct {
		name     string
		user     models.Userable
		expected []grafana.User
	}{
		{
			name: "complete user is mapped with lowercased mail",
			user: newUser(&name, &upn, &mail),
			expected: []grafana.User{
				{Login: upn, Name: name, Email: "jane.doe@example.com"},
			},
		},
		{
			name:     "user without principal name is skipped",
			user:     newUser(&name, nil, &mail),
			expected: nil,
		},
		{
			name: "user without display name is kept with empty name",
			user: newUser(nil, &upn, &mail),
			expected: []grafana.User{
				{Login: upn, Name: "", Email: "jane.doe@example.com"},
			},
		},
		{
			name:     "user without mail is skipped",
			user:     newUser(&name, &upn, nil),
			expected: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			collection := models.NewUserCollectionResponse()
			collection.SetValue([]models.Userable{test.user})

			grafanaUsers := grafana.Users{}
			u := &users{grafanaUsers: &grafanaUsers}

			var result models.UserCollectionResponseable = collection
			u.processUserResult(&result)

			if len(grafanaUsers) != len(test.expected) {
				t.Fatalf("expected %d users, got %d: %+v", len(test.expected), len(grafanaUsers), grafanaUsers)
			}
			for i, expected := range test.expected {
				if grafanaUsers[i].Login != expected.Login || grafanaUsers[i].Name != expected.Name || grafanaUsers[i].Email != expected.Email {
					t.Errorf("user %d: got %+v, want %+v", i, grafanaUsers[i], expected)
				}
			}
		})
	}
}
