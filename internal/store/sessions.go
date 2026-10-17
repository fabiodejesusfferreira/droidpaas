package store

import (
	"context"
	"database/sql"
	"errors"
)

// Session guarda só o SHA-256 do token do cookie (TSD §13.2). Os tempos são epoch ms.
type Session struct {
	ID         string
	UserID     int64
	CSRFSecret string
	CreatedAt  int64
	LastSeenAt int64
	ExpiresAt  int64
	UserAgent  string
	IP         string
}

func (s *Store) CreateSession(ctx context.Context, x Session) error {
	_, err := s.DB.ExecContext(ctx, `insert into sessions(id,user_id,csrf_secret,created_at,last_seen_at,expires_at,user_agent,ip) values(?,?,?,?,?,?,?,?)`,
		x.ID, x.UserID, x.CSRFSecret, x.CreatedAt, x.LastSeenAt, x.ExpiresAt, x.UserAgent, x.IP)
	return err
}

func (s *Store) SessionByID(ctx context.Context, id string) (Session, error) {
	var x Session
	var ua, ip sql.NullString
	err := s.DB.QueryRowContext(ctx, `select id,user_id,csrf_secret,created_at,last_seen_at,expires_at,user_agent,ip from sessions where id=?`, id).
		Scan(&x.ID, &x.UserID, &x.CSRFSecret, &x.CreatedAt, &x.LastSeenAt, &x.ExpiresAt, &ua, &ip)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	x.UserAgent, x.IP = ua.String, ip.String
	return x, err
}

func (s *Store) TouchSession(ctx context.Context, id string, lastSeen int64) error {
	_, err := s.DB.ExecContext(ctx, `update sessions set last_seen_at=? where id=?`, lastSeen, id)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `delete from sessions where id=?`, id)
	return err
}

// DeleteOtherSessions encerra todas as sessões do usuário menos a atual e devolve quantas eram.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID int64, keep string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `delete from sessions where user_id=? and id<>?`, userID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) DeleteAllSessions(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `delete from sessions where user_id=?`, userID)
	return err
}

// PurgeSessions apaga as sessões absolutamente expiradas ou ociosas desde antes de idleCutoff.
func (s *Store) PurgeSessions(ctx context.Context, now, idleCutoff int64) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `delete from sessions where expires_at<? or last_seen_at<?`, now, idleCutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) CountSessions(ctx context.Context, userID int64) (n int, err error) {
	err = s.DB.QueryRowContext(ctx, `select count(*) from sessions where user_id=?`, userID).Scan(&n)
	return
}

// ---- usuários ----

func (s *Store) UserByName(ctx context.Context, name string) (id int64, hash string, err error) {
	err = s.DB.QueryRowContext(ctx, `select id,password_hash from users where username=?`, name).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) UserHash(ctx context.Context, id int64) (hash string, err error) {
	err = s.DB.QueryRowContext(ctx, `select password_hash from users where id=?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SetPassword(ctx context.Context, id int64, hash string, nowMS int64) error {
	_, err := s.DB.ExecContext(ctx, `update users set password_hash=?, updated_at=? where id=?`, hash, nowMS, id)
	return err
}
