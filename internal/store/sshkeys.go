package store

import (
	"context"
	"time"
)

type SSHKey struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Store) AddSSHKey(ctx context.Context, userID int64, name, pub, fp string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `insert into ssh_keys(user_id,name,public_key,fingerprint,created_at) values(?,?,?,?,?)`, userID, name, pub, fp, time.Now().UnixMilli())
	if isUnique(err) {
		return 0, ErrExists
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	rows, err := s.DB.QueryContext(ctx, `select id,name,public_key,fingerprint,created_at from ssh_keys order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSHKey{}
	for rows.Next() {
		var k SSHKey
		if err := rows.Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSSHKey(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `delete from ssh_keys where id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
