package api

import (
	"errors"
	"net/http"
	"strings"

	"droidpaas/internal/gitsrv"
	"droidpaas/internal/store"
)

func (s *Server) syncKeys(w http.ResponseWriter, r *http.Request) bool {
	if s.Git == nil {
		return true
	}
	if err := s.Git.SyncKeys(r.Context()); err != nil {
		s.Events.Error(r.Context(), "git.keys_sync_failed", nil, "falha ao gravar o authorized_keys", map[string]any{"err": err.Error()})
		s.internalErr(w, err)
		return false
	}
	return true
}

func (s *Server) registerSSHKeys(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/ssh-keys", s.full(func(w http.ResponseWriter, r *http.Request) {
		ks, err := s.Store.SSHKeys(r.Context())
		if err != nil {
			s.internalErr(w, err)
			return
		}
		writeJSON(w, 200, ks)
	}))
	mux.HandleFunc("POST /api/v1/ssh-keys", s.full(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name      string `json:"name"`
			PublicKey string `json:"public_key"`
		}
		if !decode(w, r, &in) {
			return
		}
		canon, fp, comment, err := gitsrv.ParsePublicKey(in.PublicKey)
		if err != nil {
			failD(w, 422, "validation", err.Error(), map[string]any{"field": "public_key"})
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = comment
		}
		if name == "" || len(name) > 64 {
			failD(w, 422, "validation", "informe um nome (até 64 caracteres)", map[string]any{"field": "name"})
			return
		}
		id, err := s.Store.AddSSHKey(r.Context(), who(r).UserID, name, canon, fp)
		if errors.Is(err, store.ErrExists) {
			fail(w, 409, "conflict", "essa chave já está cadastrada")
			return
		}
		if err != nil {
			s.internalErr(w, err)
			return
		}
		s.Events.Info(r.Context(), "sshkey.added", nil, "chave SSH adicionada: "+name, map[string]any{"id": id, "fingerprint": fp})
		if !s.syncKeys(w, r) {
			return
		}
		writeJSON(w, 201, store.SSHKey{ID: id, Name: name, PublicKey: canon, Fingerprint: fp})
	}))
	mux.HandleFunc("DELETE /api/v1/ssh-keys/{id}", s.full(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r)
		if !ok {
			fail(w, 400, "bad_request", "id inválido")
			return
		}
		if err := s.Store.DeleteSSHKey(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fail(w, 404, "key_not_found", "chave inexistente")
				return
			}
			s.internalErr(w, err)
			return
		}
		s.Events.Info(r.Context(), "sshkey.removed", nil, "chave SSH removida", map[string]any{"id": id})
		if s.syncKeys(w, r) {
			w.WriteHeader(204)
		}
	}))
}
