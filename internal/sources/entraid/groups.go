// SPDX-FileCopyrightText: 2025 Sebastian Küthe and (other) contributors to project grafana-oss-team-sync <https://github.com/skuethe/grafana-oss-team-sync>
// SPDX-License-Identifier: GPL-3.0-or-later

package entraid

import (
	"context"
	"log/slog"
	"strings"

	abstractions "github.com/microsoft/kiota-abstractions-go"
	graph "github.com/microsoftgraph/msgraph-sdk-go"
	graphgroups "github.com/microsoftgraph/msgraph-sdk-go/groups"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/skuethe/grafana-oss-team-sync/internal/config"
	"github.com/skuethe/grafana-oss-team-sync/internal/grafana"
	"github.com/skuethe/grafana-oss-team-sync/internal/sources/sourcetypes"
)

type groups struct {
	client       *graph.GraphServiceClient
	headers      *abstractions.RequestHeaders
	grafanaTeams *grafana.Teams
}

func (g *groups) processGroupResult(result *models.GroupCollectionResponseable) {
	for _, group := range (*result).GetValue() {
		groupDisplayName := group.GetDisplayName()
		groupId := *group.GetId()
		groupMail := group.GetMail()

		var mail string
		if groupMail != nil {
			mail = strings.ToLower(*groupMail)
		}

		var displayName string
		if groupDisplayName != nil {
			displayName = *groupDisplayName
		}

		groupLog := slog.With(
			slog.Group("group",
				slog.String("displayname", displayName),
				slog.String("id", groupId),
			),
		)
		if groupDisplayName == nil {
			groupLog.Warn("EntraID group is missing a display name, skipping")
			continue
		}
		groupLog.Info("found EntraID group")

		// A group can be configured multiple times to sync into several Grafana organizations
		// (once per "orgId" entry), so resolve every matching configuration entry.
		configuredTeams := config.Instance.Teams.FindAll(*groupDisplayName)
		if len(configuredTeams) == 0 {
			groupLog.Warn("EntraID group has no matching team configuration entry, skipping")
			continue
		}

		// Process users once per EntraID group, the resulting list is reused for every
		// organization this group is configured to sync into.
		grafanaUserList := g.ProcessUsers(&groupId)

		for _, team := range configuredTeams {
			for _, orgID := range team.TargetOrgs() {
				*g.grafanaTeams = append(*g.grafanaTeams, grafana.Team{
					Parameter: &grafana.TeamParameter{
						Name:  groupDisplayName,
						Email: mail,
					},
					Users: grafanaUserList,
					OrgID: orgID,
					Role:  team.RoleFor(orgID),
				})
			}
		}
		config.Instance.Teams = config.Instance.Teams.Remove(*groupDisplayName)
	}
}

func (g *groups) handleGroupPagination(nextLink *string) (*models.GroupCollectionResponseable, error) {
	configuration := &graphgroups.GroupsRequestBuilderGetRequestConfiguration{
		Headers: g.headers,
	}
	result, err := g.client.Groups().WithUrl(*nextLink).Get(context.Background(), configuration)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// buildGroupFilter creates an OData $filter that matches any group whose
// displayName is exactly one of the given teams.
func buildGroupFilter(teams []string) string {
	return "displayName in ('" + strings.Join(teams, "', '") + "')"
}

// buildGroupSearch creates an OData $search that matches any group whose
// displayName contains all tokens of one of the given teams, e.g.
// "displayName:a" OR "displayName:b"
// Double quotes and backslashes have to be escaped inside a $search clause.
// See: https://learn.microsoft.com/en-us/graph/search-query-parameter#use-search-on-directory-object-collections
func buildGroupSearch(teams []string) string {
	escaper := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	clauses := make([]string, len(teams))
	for i, team := range teams {
		clauses[i] = `"displayName:` + escaper.Replace(team) + `"`
	}
	return strings.Join(clauses, " OR ")
}

func (g *groups) getInitialGroupRequest() (*models.GroupCollectionResponseable, error) {

	requestCount := true
	requestParams := &graphgroups.GroupsRequestBuilderGetQueryParameters{
		Select: []string{"id", "displayName", "mail"},
		Count:  &requestCount,
	}
	teamNames := config.Instance.Teams.Names()
	if config.Instance.Features.EntraIdUseSearchInsteadOfFilter {
		requestSearch := buildGroupSearch(teamNames)
		requestParams.Search = &requestSearch
	} else {
		requestFilter := buildGroupFilter(teamNames)
		requestParams.Filter = &requestFilter
	}
	configuration := &graphgroups.GroupsRequestBuilderGetRequestConfiguration{
		Headers:         g.headers,
		QueryParameters: requestParams,
	}
	result, err := g.client.Groups().Get(context.Background(), configuration)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func ProcessGroups(instance *sourcetypes.SourcePlugin) *grafana.Teams {
	groupsLog := slog.With(slog.String("package", "entraid.groups"))
	groupsLog.Info("processing EntraID groups")

	headers := abstractions.NewRequestHeaders()
	headers.Add("ConsistencyLevel", "eventual")

	g := groups{
		client:       instance.EntraID,
		headers:      headers,
		grafanaTeams: &grafana.Teams{},
	}

	gr, err := g.getInitialGroupRequest()
	if err != nil {
		groupsLog.Error("could not get initial group result from EntraID")
		panic(err)
	}

	countFound := (*gr).GetOdataCount()

	for {
		// Handle group result
		g.processGroupResult(gr)

		// Handle possible pagination
		nextPageUrl := (*gr).GetOdataNextLink()
		if nextPageUrl != nil {
			groupsLog.Debug("processing paginated group result")
			gr, err = g.handleGroupPagination(nextPageUrl)
			if err != nil {
				groupsLog.Error("could not get paged group result from EntraID")
				panic(err)
			}
		} else {
			break
		}
	}

	if len(config.Instance.Teams) > 0 {
		groupsLog.Warn("could not find the following groups in EntraID", "skipped", strings.Join(config.Instance.Teams.Names(), ","))
	}

	groupsLog.Info("finished processing EntraID groups",
		slog.Group("groups",
			slog.Int64("found", *countFound),
		),
	)

	return g.grafanaTeams
}
