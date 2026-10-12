package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Release: snapshot de código+dependências (TSD §7). Runtime registra o runtime resolvido no build
// (não existe no DDL v0.1; vai na migração 0003).
type Release struct {
	ID        int64  `json:"id"`
	AppID     int64  `json:"app_id"`
	GitSHA    string `json:"git_sha"`
	GitRef    string `json:"git_ref"`
	Message   string `json:"message"`
	Status    string `json:"status"` // building|ready|failed|pruned
	Path      string `json:"path"`
	SizeBytes *int64 `json:"size_bytes"`
	Runtime   string `json:"runtime"`
	CreatedAt int64  `json:"created_at"`
}

const relCols = `id,app_id,git_sha,git_ref,coalesce(message,''),status,path,size_bytes,runtime,created_at`

func scanRelease(sc interface{ Scan(...any) error }) (Release, error) {
	var r Release
	err := sc.Scan(&r.ID, &r.AppID, &r.GitSHA, &r.GitRef, &r.Message, &r.Status, &r.Path, &r.SizeBytes, &r.Runtime, &r.CreatedAt)
	return r, err
}

func (s *Store) InsertRelease(ctx context.Context, r *Release) error {
	r.CreatedAt = time.Now().UnixMilli()
	res, err := s.DB.ExecContext(ctx, `insert into releases(app_id,git_sha,git_ref,message,status,path,runtime,created_at) values(?,?,?,?,?,?,?,?)`,
		r.AppID, r.GitSHA, r.GitRef, r.Message, r.Status, r.Path, r.Runtime, r.CreatedAt)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) Release(ctx context.Context, id int64) (Release, error) {
	r, err := scanRelease(s.DB.QueryRowContext(ctx, `select `+relCols+` from releases where id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *Store) UpdateRelease(ctx context.Context, r Release) error {
	_, err := s.DB.ExecContext(ctx, `update releases set status=?,runtime=?,size_bytes=?,message=? where id=?`, r.Status, r.Runtime, r.SizeBytes, r.Message, r.ID)
	return err
}

// Releases devolve as releases do app, da mais nova para a mais antiga.
func (s *Store) Releases(ctx context.Context, appID int64, status string) ([]Release, error) {
	q, args := `select `+relCols+` from releases where app_id=?`, []any{appID}
	if status != "" {
		q += ` and status=?`
		args = append(args, status)
	}
	rows, err := s.DB.QueryContext(ctx, q+` order by id desc`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
