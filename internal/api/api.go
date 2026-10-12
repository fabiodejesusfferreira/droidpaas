// Package api: handlers HTTP do droidd. Na 2A só liveness e ping interno; o resto vem na 2B.
package api

import (
	"encoding/json"
	"net/http"
)

// Public é o mux de 127.0.0.1:8090 (painel/API).
func Public(version string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version})
	})
	return mux
}

// Internal é o mux do socket Unix 0600 (hook do git e CLI local).
func Internal() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("pong\n"))
	})
	return mux
}
