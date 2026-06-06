// Package server exposes the probe monitor over HTTP: a JSON status API, a
// Server-Sent Events stream for live updates, an endpoint to set the target
// temperature, and the embedded single-page web UI.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/awlx/meater-golang/internal/monitor"
)

//go:embed web
var webFS embed.FS

// Server wires HTTP handlers to a Monitor.
type Server struct {
	mon *monitor.Monitor
	mux *http.ServeMux
}

// New builds a Server backed by the given monitor.
func New(mon *monitor.Monitor) *Server {
	s := &Server{mon: mon, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	sub, _ := fs.Sub(webFS, "web")
	s.mux.Handle("/", http.FileServer(http.FS(sub)))
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/history", s.handleHistory)
	s.mux.HandleFunc("/api/stream", s.handleStream)
	s.mux.HandleFunc("/api/target", s.handleTarget)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.Status())
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.History())
}

// handleTarget sets the target tip temperature. Accepts JSON {"celsius": N} or
// {"fahrenheit": N}, or a query parameter ?celsius=N / ?fahrenheit=N.
func (s *Server) handleTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Celsius    *float64 `json:"celsius"`
		Fahrenheit *float64 `json:"fahrenheit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	celsius, ok := resolveTarget(r, body.Celsius, body.Fahrenheit)
	if !ok {
		http.Error(w, "provide celsius or fahrenheit", http.StatusBadRequest)
		return
	}
	if celsius < 0 || celsius > 300 {
		http.Error(w, "target out of range", http.StatusBadRequest)
		return
	}

	s.mon.SetTarget(celsius)
	writeJSON(w, http.StatusOK, s.mon.Status())
}

// handleStream streams status updates as Server-Sent Events.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	updates, cancel := s.mon.Subscribe()
	defer cancel()

	// Send the current state immediately so the UI populates on connect.
	writeEvent(w, flusher, s.mon.Status())

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case status, open := <-updates:
			if !open {
				return
			}
			writeEvent(w, flusher, status)
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func resolveTarget(r *http.Request, c, f *float64) (float64, bool) {
	if c != nil {
		return *c, true
	}
	if f != nil {
		return (*f - 32) * 5 / 9, true
	}
	if v := r.URL.Query().Get("celsius"); v != "" {
		var n float64
		if _, err := fmt.Sscanf(v, "%g", &n); err == nil {
			return n, true
		}
	}
	if v := r.URL.Query().Get("fahrenheit"); v != "" {
		var n float64
		if _, err := fmt.Sscanf(v, "%g", &n); err == nil {
			return (n - 32) * 5 / 9, true
		}
	}
	return 0, false
}

func writeEvent(w http.ResponseWriter, f http.Flusher, status monitor.Status) {
	data, err := json.Marshal(status)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
