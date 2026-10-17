package api

import (
	"errors"
	"net/http"

	"droidpaas/internal/store"
)

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
		k, err := s.AddSSHKey(r.Context(), who(r).UserID, in.Name, in.PublicKey)
		switch {
		case errors.Is(err, ErrKeyExists):
			fail(w, 409, "conflict", err.Error())
		case err != nil:
			s.appErr(w, err)
		default:
			writeJSON(w, 201, k)
		}
	}))
	mux.HandleFunc("DELETE /api/v1/ssh-keys/{id}", s.full(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r)
		if !ok {
			fail(w, 400, "bad_request", "id inválido")
			return
		}
		if err := s.RemoveSSHKey(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fail(w, 404, "key_not_found", "chave inexistente")
				return
			}
			s.internalErr(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
