package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"droidpaas/internal/apps"
	"droidpaas/internal/auth"
	"droidpaas/internal/gitsrv"
	"droidpaas/internal/store"
)

// Operações de domínio compartilhadas pela API REST e pelo painel web: as regras ficam num lugar só.

var ErrKeyExists = errors.New("essa chave já está cadastrada")

// ApplySettings valida e grava as configurações editáveis (base_domain, keep_releases…).
func (s *Server) ApplySettings(ctx context.Context, in map[string]string) error {
	for k, v := range in {
		val, ok := settingValidators[k]
		if !ok || !val(v) {
			return &apps.ValidationError{Field: k, Msg: "configuração inválida"}
		}
	}
	for k, v := range in {
		if err := s.Store.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	if _, ok := in["base_domain"]; ok {
		if err := s.Apps.SyncAutoDomains(ctx); err != nil {
			return err
		}
	}
	s.Events.Info(ctx, "settings.updated", nil, "configurações alteradas", map[string]any{"keys": keys(in)})
	s.wake()
	return nil
}

// SetTunnelToken guarda o token do túnel cifrado; vazio remove.
func (s *Server) SetTunnelToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		if err := s.Store.DeleteSetting(ctx, "tunnel_token"); err != nil {
			return err
		}
		s.Events.Info(ctx, "tunnel.cleared", nil, "token do túnel removido", nil)
	} else {
		if len(token) > 4096 || strings.ContainsAny(token, " \r\n\t\x00") {
			return &apps.ValidationError{Field: "token", Msg: "token inválido"}
		}
		sealed, err := s.Box.Seal([]byte(token))
		if err != nil {
			return err
		}
		if err := s.Store.SetSetting(ctx, "tunnel_token", string(sealed)); err != nil {
			return err
		}
		s.Events.Info(ctx, "tunnel.configured", nil, "token do túnel definido", nil)
	}
	s.wake()
	return nil
}

// AddSSHKey valida a chave pública, grava e regrava o authorized_keys.
func (s *Server) AddSSHKey(ctx context.Context, userID int64, name, pub string) (store.SSHKey, error) {
	canon, fp, comment, err := gitsrv.ParsePublicKey(pub)
	if err != nil {
		return store.SSHKey{}, &apps.ValidationError{Field: "public_key", Msg: err.Error()}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = comment
	}
	if name == "" || len(name) > 64 {
		return store.SSHKey{}, &apps.ValidationError{Field: "name", Msg: "informe um nome (até 64 caracteres)"}
	}
	id, err := s.Store.AddSSHKey(ctx, userID, name, canon, fp)
	if errors.Is(err, store.ErrExists) {
		return store.SSHKey{}, ErrKeyExists
	}
	if err != nil {
		return store.SSHKey{}, err
	}
	s.Events.Info(ctx, "sshkey.added", nil, "chave SSH adicionada: "+name, map[string]any{"id": id, "fingerprint": fp})
	return store.SSHKey{ID: id, Name: name, PublicKey: canon, Fingerprint: fp}, s.syncKeysCtx(ctx)
}

func (s *Server) RemoveSSHKey(ctx context.Context, id int64) error {
	if err := s.Store.DeleteSSHKey(ctx, id); err != nil {
		return err
	}
	s.Events.Info(ctx, "sshkey.removed", nil, "chave SSH removida", map[string]any{"id": id})
	return s.syncKeysCtx(ctx)
}

func (s *Server) syncKeysCtx(ctx context.Context) error {
	if s.Git == nil {
		return nil
	}
	if err := s.Git.SyncKeys(ctx); err != nil {
		s.Events.Error(ctx, "git.keys_sync_failed", nil, "falha ao gravar o authorized_keys", map[string]any{"err": err.Error()})
		return fmt.Errorf("gravar o authorized_keys: %w", err)
	}
	return nil
}

// CreateToken cria um token de API; o valor em claro só existe no retorno.
func (s *Server) CreateToken(ctx context.Context, userID int64, name, scope string) (id int64, token string, err error) {
	if scope == "" {
		scope = "full"
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || (scope != "full" && scope != "git") {
		return 0, "", &apps.ValidationError{Field: "name", Msg: "nome (1–64 caracteres) obrigatório; escopo: full ou git"}
	}
	tok, hash, prefix, err := auth.NewToken()
	if err != nil {
		return 0, "", err
	}
	id, err = s.Store.CreateToken(ctx, userID, name, hash, prefix, scope)
	if err != nil {
		return 0, "", err
	}
	s.Events.Info(ctx, "token.created", nil, "token de API criado", map[string]any{"id": id, "name": name, "scope": scope})
	return id, tok, nil
}

func (s *Server) RevokeToken(ctx context.Context, userID, id int64) error {
	if err := s.Store.RevokeToken(ctx, userID, id); err != nil {
		return err
	}
	s.Events.Info(ctx, "token.revoked", nil, "token de API revogado", map[string]any{"id": id})
	return nil
}
