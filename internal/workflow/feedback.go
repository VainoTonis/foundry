package workflow

import (
	"encoding/json"
	"strings"
)

type phaseFeedbackPayload struct {
	Result        string   `json:"result"`
	UsefulContext []string `json:"useful_context"`
	Problems      []string `json:"problems"`
	Confidence    float64  `json:"confidence"`
}

func buildPhaseFeedback(verdict, notes string, filesJSON []byte, commitHash string) []byte {
	feedback := phaseFeedbackPayload{Result: strings.TrimSpace(verdict), Confidence: 0.6}
	if feedback.Result == "pass" {
		feedback.Confidence = 0.85
	} else if feedback.Result == "fail" {
		feedback.Confidence = 0.4
	}
	var files []string
	if len(filesJSON) > 0 {
		_ = json.Unmarshal(filesJSON, &files)
	}
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			feedback.UsefulContext = append(feedback.UsefulContext, "touched "+f)
		}
	}
	if commitHash = strings.TrimSpace(commitHash); commitHash != "" {
		feedback.UsefulContext = append(feedback.UsefulContext, "commit "+commitHash)
	}
	if notes = strings.TrimSpace(notes); notes != "" {
		if feedback.Result == "fail" {
			feedback.Problems = append(feedback.Problems, notes)
		} else {
			feedback.UsefulContext = append(feedback.UsefulContext, notes)
		}
	}
	b, err := json.Marshal(feedback)
	if err != nil {
		return []byte(`{"result":"` + verdict + `","useful_context":[],"problems":["phase feedback marshal failed"],"confidence":0}`)
	}
	return b
}

func extractFilesJSON(reviewOut string) []byte {
	var files []string
	for _, line := range strings.Split(reviewOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "commit") || strings.HasPrefix(line, "status") {
			continue
		}
		files = append(files, line)
	}
	if len(files) == 0 {
		return []byte("[]")
	}
	b := []byte(`["`)
	b = append(b, []byte(strings.Join(files, `","`))...)
	b = append(b, []byte(`"]`)...)
	return b
}
