package review

import (
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

const maxBody = 16 << 20

// Handler serves the page and its API.
func Handler(s *Store, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(indexHTML) // a client that went away needs no reply
	})
	mux.HandleFunc("GET /api/items", func(w http.ResponseWriter, _ *http.Request) {
		items, err := s.List()
		reply(w, log, items, err)
	})
	mux.HandleFunc("POST /api/check", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		problems, candidate, err := s.Check(req.Kind, req.Case)
		if err != nil {
			problems, err = []string{err.Error()}, nil
		}
		reply(w, log, map[string]any{"problems": problems, "candidate": candidate}, err)
	})
	mux.HandleFunc("POST /api/save", func(w http.ResponseWriter, r *http.Request) {
		if req, ok := decode(w, r); ok {
			it, err := s.Save(req.Kind, req.ID, req.Case)
			reply(w, log, it, err)
		}
	})
	mux.HandleFunc("POST /api/approve", func(w http.ResponseWriter, r *http.Request) {
		if req, ok := decode(w, r); ok {
			it, err := s.Approve(req.Kind, req.ID, req.Case, req.Force)
			reply(w, log, it, err)
		}
	})
	mux.HandleFunc("POST /api/reject", func(w http.ResponseWriter, r *http.Request) {
		if req, ok := decode(w, r); ok {
			it, err := s.Reject(req.Kind, req.ID, req.Case, req.Reason)
			reply(w, log, it, err)
		}
	})
	mux.HandleFunc("POST /api/restore", func(w http.ResponseWriter, r *http.Request) {
		if req, ok := decode(w, r); ok {
			it, err := s.Restore(req.Kind, req.ID)
			reply(w, log, it, err)
		}
	})
	return mux
}

type request struct {
	Kind   string          `json:"kind"`
	ID     string          `json:"id"`
	Case   json.RawMessage `json:"case"`
	Force  bool            `json:"force"`
	Reason string          `json:"reason"`
}

func decode(w http.ResponseWriter, r *http.Request) (request, bool) {
	var req request
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return req, false
	}
	if req.Kind == "" {
		http.Error(w, "bad request: kind is required", http.StatusBadRequest)
		return req, false
	}
	if len(req.Case) == 0 || string(req.Case) == "null" {
		req.Case = nil
	}
	return req, true
}

// reply writes v as JSON, or the error. A not-found or a case with problems
// is the reviewer's to fix, so its message is shown as it is.
func reply(w http.ResponseWriter, log *slog.Logger, v any, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, ErrNotFound):
			code = http.StatusNotFound
		case errors.Is(err, ErrProblems):
			code = http.StatusConflict
		case errors.Is(err, ErrBadCase):
			code = http.StatusBadRequest
		default:
			log.Error("review", "err", err)
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		log.Warn("write reply", "err", err)
	}
}
