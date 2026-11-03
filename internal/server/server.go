// Package server exposes a kv.Store over HTTP.
//
//	GET    /health              liveness probe
//	GET    /kv/{key}            read a value
//	PUT    /kv/{key}            write the request body as the value
//	DELETE /kv/{key}            delete a key
//	GET    /scan?start=&end=&limit=   ordered range scan, JSON
//	GET    /stats               key count and WAL size, JSON
package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/kv"
)

// MaxValueBytes caps a PUT body.
const MaxValueBytes = 1 << 20

// New returns the HTTP handler. Method-qualified patterns need Go 1.22+.
func New(store *kv.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /kv/{key}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Get([]byte(r.PathValue("key")))
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(v)
	})
	mux.HandleFunc("PUT /kv/{key}", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxValueBytes))
		if err != nil {
			http.Error(w, "value too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := store.Put([]byte(r.PathValue("key")), body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /kv/{key}", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete([]byte(r.PathValue("key"))); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /scan", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit <= 0 || limit > 1000 {
			limit = 100
		}
		var end []byte
		if q.Has("end") {
			end = []byte(q.Get("end"))
		}
		type pair struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		out := []pair{}
		for _, p := range store.Scan([]byte(q.Get("start")), end, limit) {
			out = append(out, pair{string(p.Key), string(p.Value)})
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]int64{
			"keys":      int64(store.Len()),
			"wal_bytes": store.LogSize(),
		})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
