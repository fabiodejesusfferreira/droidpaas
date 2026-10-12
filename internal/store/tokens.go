package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Token struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"-"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Scope      string `json:"scope"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	RevokedAt  *int64 `json:"revoked_at"`
}

func (s *Store) CreateToken(ctx context.Context, userID int64, name, hash, prefix, scope string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `insert into api_tokens(user_id, name, token_hash, prefix, scope, created_at) values(?,?,?,?,?,?)`,
		userID, name, hash, prefix, scope, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// TokenByHash devolve só tokens não revogados.
func (s *Store) TokenByHash(ctx context.Context, hash string) (Token, error) {
	var t Token
	err := s.DB.QueryRowContext(ctx, `select id,user_id,name,prefix,scope,created_at,last_used_at from api_tokens where token_hash=? and revoked_at is null`, hash).
		Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.Scope, &t.CreatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) TouchToken(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `update api_tokens set last_used_at=? where id=?`, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) ListTokens(ctx context.Context, userID int64) ([]Token, error) {
	rows, err := s.DB.QueryContext(ctx, `select id,user_id,name,prefix,scope,created_at,last_used_at,revoked_at from api_tokens where user_id=? order by id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.Scope, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RevokeToken(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `update api_tokens set revoked_at=? where id=? and user_id=? and revoked_at is null`, time.Now().UnixMilli(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) User(ctx context.Context, id int64) (name string, err error) {
	err = s.DB.QueryRowContext(ctx, `select username from users where id=?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}
