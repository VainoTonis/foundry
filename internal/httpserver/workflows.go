package httpserver

import (
	"net/http"
	"strconv"
	"strings"
)

// ---- workflows ----

func (s *Server) handleWorkflow(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/workflows/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	if suffix == "stream" {
		s.streamWorkflow(w, r, id)
		return
	}
	s.jsonAPI.HandleWorkflow(w, r)
}
