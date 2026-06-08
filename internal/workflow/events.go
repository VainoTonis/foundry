package workflow

import (
	"encoding/json"
	"fmt"
	"time"
)

func (r *Runner) publishLog(workflowID, phaseID int64, line string) {
	if r.hub == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"event":    "log",
		"phase_id": phaseID,
		"line":     line,
		"ts":       time.Now().Format(time.RFC3339),
	})
	r.hub.Publish(fmt.Sprintf("wf:%d", workflowID), data)
}

func (r *Runner) publishPhaseUpdate(workflowID, phaseID int64, status string) {
	if r.hub == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"event":       "phase_update",
		"workflow_id": workflowID,
		"phase_id":    phaseID,
		"status":      status,
		"ts":          time.Now().Format(time.RFC3339),
	})
	r.hub.Publish(fmt.Sprintf("wf:%d", workflowID), data)
}

func (r *Runner) publishWorkflowUpdate(workflowID int64, status string) {
	if r.hub == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"event":       "workflow_update",
		"workflow_id": workflowID,
		"status":      status,
		"ts":          time.Now().Format(time.RFC3339),
	})
	r.hub.Publish(fmt.Sprintf("wf:%d", workflowID), data)
}
