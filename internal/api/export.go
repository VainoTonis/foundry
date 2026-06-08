package api

import (
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

// ---- export ----

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type exportPhase struct {
		db.Phase
		Logs []db.PhaseLog `json:"logs"`
	}
	type exportWorkflow struct {
		db.Workflow
		Phases []exportPhase `json:"phases"`
	}
	type exportSpec struct {
		db.Spec
		Workflows []exportWorkflow `json:"workflows"`
	}
	type exportPayload struct {
		Projects         []db.Project         `json:"projects"`
		Specs            []exportSpec         `json:"specs"`
		MemoryUpdateJobs []db.MemoryUpdateJob `json:"memory_update_jobs"`
		SpecDrafts       []db.SpecDraft       `json:"spec_drafts"`
		Profiles         []db.Profile         `json:"profiles"`
	}

	ctx := r.Context()
	fail := func(err error) bool {
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		return false
	}

	projects, err := db.ListProjects(ctx, s.pool)
	if fail(err) {
		return
	}
	specs, err := db.ListSpecs(ctx, s.pool, db.ListSpecsFilter{})
	if fail(err) {
		return
	}

	exportSpecs := make([]exportSpec, 0, len(specs))
	for _, spec := range specs {
		workflows, err := db.ListWorkflowsBySpec(ctx, s.pool, spec.ID)
		if fail(err) {
			return
		}
		exportWorkflows := make([]exportWorkflow, 0, len(workflows))
		for _, workflow := range workflows {
			phases, err := db.ListPhasesByWorkflow(ctx, s.pool, workflow.ID)
			if fail(err) {
				return
			}
			exportPhases := make([]exportPhase, 0, len(phases))
			for _, phase := range phases {
				logs, err := db.ListPhaseLogs(ctx, s.pool, phase.ID)
				if fail(err) {
					return
				}
				exportPhases = append(exportPhases, exportPhase{Phase: phase, Logs: logs})
			}
			exportWorkflows = append(exportWorkflows, exportWorkflow{Workflow: workflow, Phases: exportPhases})
		}
		exportSpecs = append(exportSpecs, exportSpec{Spec: spec, Workflows: exportWorkflows})
	}

	memoryUpdateJobs, err := db.ListMemoryUpdateJobs(ctx, s.pool)
	if fail(err) {
		return
	}
	specDrafts, err := db.ListSpecDrafts(ctx, s.pool)
	if fail(err) {
		return
	}
	profiles, err := db.ListProfiles(ctx, s.pool)
	if fail(err) {
		return
	}

	jsonOK(w, exportPayload{Projects: projects, Specs: exportSpecs, MemoryUpdateJobs: memoryUpdateJobs, SpecDrafts: specDrafts, Profiles: profiles}, http.StatusOK)
}
