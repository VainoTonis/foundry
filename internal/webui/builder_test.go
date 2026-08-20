package webui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// TestBuilderStartTemplateUsesRepositoryTerminology covers that the
// spec-builder start form is expressed entirely in repository
// terminology: the select field posting a new draft's owner must be
// named repository_id and rendered from a Repositories slice, with no
// leftover legacy naming or label in the markup.
func TestBuilderStartTemplateUsesRepositoryTerminology(t *testing.T) {
	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "builder.start", struct {
		Repositories []repository.Repository
		Drafts       []db.SpecDraft
	}{
		Repositories: []repository.Repository{{ID: 1, Name: "demo-repo"}},
		Drafts:       nil,
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `name="repository_id"`) {
		t.Fatalf("builder.start output missing repository_id select field:\n%s", out)
	}
	legacyFieldName := "project" + "_id"
	if strings.Contains(out, legacyFieldName) {
		t.Fatalf("builder.start output must not reference legacy %s:\n%s", legacyFieldName, out)
	}
	legacyLabel := ">" + "Project" + "<"
	if strings.Contains(out, legacyLabel) {
		t.Fatalf("builder.start output must not use legacy %s label:\n%s", legacyLabel, out)
	}
	if !strings.Contains(out, "demo-repo") {
		t.Fatalf("builder.start output missing rendered repository option:\n%s", out)
	}
}

// TestBuilderDetailTemplateLinksToRepositoriesRoute covers that the
// draft detail page's context link points at the canonical /repositories
// route (and Repository label) instead of the removed legacy route,
// when the draft has an attached repository.
func TestBuilderDetailTemplateLinksToRepositoriesRoute(t *testing.T) {
	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "builder.detail", struct {
		Draft         db.SpecDraft
		Messages      []chatMessage
		Preview       string
		Repository    repository.Repository
		HasRepository bool
	}{
		Draft:         db.SpecDraft{ID: 5, Title: "demo", Status: "active", UpdatedAt: time.Now()},
		Messages:      nil,
		Preview:       "",
		Repository:    repository.Repository{ID: 9, Name: "demo-repo"},
		HasRepository: true,
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "/repositories/9") {
		t.Fatalf("builder.detail output missing /repositories/9 link:\n%s", out)
	}
	legacyRoute := "/proj" + "ects/"
	if strings.Contains(out, legacyRoute) {
		t.Fatalf("builder.detail output must not link to legacy %s route:\n%s", legacyRoute, out)
	}
	if !strings.Contains(out, "Repository: demo-repo") {
		t.Fatalf("builder.detail output missing Repository label:\n%s", out)
	}
}
