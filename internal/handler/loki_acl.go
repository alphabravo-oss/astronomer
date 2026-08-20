package handler

import (
	"encoding/json"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
)

func buildLokiQueryACLFromCandidates(admins []sqlc.ListLokiQueryACLAdminCandidatesRow, users []sqlc.ListLokiQueryACLUserCandidatesRow) lokiauth.QueryACL {
	acl := lokiauth.QueryACL{Users: map[string][]string{}}
	adminSet := map[string]struct{}{}
	for _, row := range admins {
		email := strings.ToLower(strings.TrimSpace(row.Email))
		if email == "" {
			continue
		}
		if row.IsSuperuser || rbacRulesGrant(row.Rules, []string{"monitoring", "*"}, []string{"update", "*"}) {
			if _, ok := adminSet[email]; ok {
				continue
			}
			adminSet[email] = struct{}{}
			acl.Admins = append(acl.Admins, email)
		}
	}
	for _, row := range users {
		email := strings.ToLower(strings.TrimSpace(row.Email))
		if email == "" {
			continue
		}
		if _, isAdmin := adminSet[email]; isAdmin {
			continue
		}
		if !rbacRulesGrant(row.Rules, []string{"logging", "monitoring", "*"}, []string{"read", "*"}) {
			continue
		}
		cluster := row.ClusterID.String()
		if clusterInList(acl.Users[email], cluster) {
			continue
		}
		acl.Users[email] = append(acl.Users[email], cluster)
	}
	return acl
}

func clusterInList(ids []string, cluster string) bool {
	for _, id := range ids {
		if strings.EqualFold(id, cluster) {
			return true
		}
	}
	return false
}

func rbacRulesGrant(raw json.RawMessage, resources, verbs []string) bool {
	if len(raw) == 0 {
		return false
	}
	var rules []struct {
		Resource string   `json:"resource"`
		Verbs    []string `json:"verbs"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return false
	}
	resWant := map[string]struct{}{}
	for _, r := range resources {
		resWant[strings.ToLower(r)] = struct{}{}
	}
	verbWant := map[string]struct{}{}
	for _, v := range verbs {
		verbWant[strings.ToLower(v)] = struct{}{}
	}
	for _, rule := range rules {
		if _, ok := resWant[strings.ToLower(strings.TrimSpace(rule.Resource))]; !ok {
			continue
		}
		for _, verb := range rule.Verbs {
			if _, ok := verbWant[strings.ToLower(strings.TrimSpace(verb))]; ok {
				return true
			}
		}
	}
	return false
}
