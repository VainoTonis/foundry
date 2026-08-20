package webui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

func planRepo(position int, projectID int64, name string) db.PlanRepository {
	return db.PlanRepository{
		Position:  position,
		ProjectID: projectID,
		Repository: repository.Repository{
			ID:   projectID,
			Name: name,
		},
	}
}

// TestPlansListTemplateRendersRepositories covers that the plans list
// fragment renders the primary repository name (and an "+N more"
// indicator for multi-repository plans) without panicking, for plans
// with one and with several repositories.
func TestPlansListTemplateRendersRepositories(t *testing.T) {
	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "plans.list", struct {
		Plans []db.Plan
	}{
		Plans: []db.Plan{
			{ID: 1, Title: "single-repo plan", Status: "pending", Repositories: []db.PlanRepository{planRepo(0, 10, "solo-repo")}},
			{ID: 2, Title: "multi-repo plan", Status: "pending", Repositories: []db.PlanRepository{
				planRepo(0, 20, "primary-repo"),
				planRepo(1, 21, "secondary-repo"),
				planRepo(2, 22, "tertiary-repo"),
			}},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "solo-repo") {
		t.Fatalf("plans.list output missing solo-repo:\n%s", out)
	}
	if !strings.Contains(out, "primary-repo") {
		t.Fatalf("plans.list output missing primary-repo:\n%s", out)
	}
	if !strings.Contains(out, "+2 more") {
		t.Fatalf("plans.list output missing '+2 more' indicator for multi-repo plan:\n%s", out)
	}
}

// TestPlansDetailTemplateRendersRepositories covers that the plan detail
// fragment renders all repositories in position order, marks position 0
// as primary, and does not panic for plans with one or several
// repositories.
func TestPlansDetailTemplateRendersRepositories(t *testing.T) {
	cases := []struct {
		name  string
		repos []db.PlanRepository
	}{
		{name: "single repository", repos: []db.PlanRepository{planRepo(0, 10, "solo-repo")}},
		{name: "three repositories", repos: []db.PlanRepository{
			planRepo(0, 20, "primary-repo"),
			planRepo(1, 21, "secondary-repo"),
			planRepo(2, 22, "tertiary-repo"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := templates.ExecuteTemplate(&buf, "plans.detail", struct {
				Plan  db.Plan
				Steps []db.PlanStep
			}{
				Plan: db.Plan{
					ID:           1,
					Title:        "demo plan",
					Status:       "pending",
					Repositories: tc.repos,
					CreatedAt:    time.Now(),
					UpdatedAt:    time.Now(),
				},
			})
			if err != nil {
				t.Fatalf("ExecuteTemplate() error = %v", err)
			}
			out := buf.String()
			if !strings.Contains(out, "(primary)") {
				t.Fatalf("plans.detail output missing primary marker:\n%s", out)
			}
			if !strings.Contains(out, tc.repos[0].Repository.Name) {
				t.Fatalf("plans.detail output missing primary repository name:\n%s", out)
			}
			for _, r := range tc.repos {
				if !strings.Contains(out, r.Repository.Name) {
					t.Fatalf("plans.detail output missing repository %s:\n%s", r.Repository.Name, out)
				}
			}
		})
	}
}
