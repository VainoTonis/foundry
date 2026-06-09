package stream

import (
	"fmt"
	"io"
	"net/http"
)

func StartSSE(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return flusher, true
}

func WriteIDData(w io.Writer, id int64, data []byte) {
	fmt.Fprintf(w, "id: %d\ndata: %s\n\n", id, data)
}

func WriteEvent(w io.Writer, event string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func WriteIDEvent(w io.Writer, id int64, event string, data []byte) {
	fmt.Fprintf(w, "id: %d\n", id)
	WriteEvent(w, event, data)
}

func WriteHeartbeat(w io.Writer) {
	WriteEvent(w, "heartbeat", []byte("{}"))
}

func WriteDone(w io.Writer) {
	WriteEvent(w, "done", []byte("{}"))
}
