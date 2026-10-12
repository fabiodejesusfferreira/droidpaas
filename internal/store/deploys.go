package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Deploy struct {
	ID         int64   `json:"id"`
	AppID      int64   `json:"app_id"`
	ReleaseID  *int64  `json:"release_id"`
	Trigger    string  `json:"trigger"` // git_push|manual|rollback|rebuild
	Status     string  `json:"status"`  // queued|running|succeeded|failed|rolled_back|cancelled|interrupted
	Stage      *string `json:"stage"`
	Error      *string `json:"error"`
	LogPath    string  `json:"-"`
	GitSHA     string  `json:"git_sha"`
	GitRef     string  `json:"git_ref"`
	Rebuild    bool    `json:"rebuild"`
	QueuedAt   int64   `json:"queued_at"`
	StartedAt  *int64  `json:"started_at"`
	FinishedAt *int64  `json:"finished_at"`
}

const depCols = `id,app_id,release_id,trigger,status,stage,error,log_path,git_sha,git_ref,rebuild,queued_at,started_at,finished_at`

func scanDeploy(sc interface{ Scan(...any) error }) (Deploy, error) {
	var d Deploy
	err := sc.Scan(&d.ID, &d.AppID, &d.ReleaseID, &d.Trigger, &d.Status, &d.Stage, &d.Error, &d.LogPath, &d.GitSHA, &d.GitRef, &d.Rebuild, &d.QueuedAt, &d.StartedAt, &d.FinishedAt)
	return d, err
}

func (s *Store) InsertDeploy(ctx context.Context, d *Deploy) error {
	d.QueuedAt, d.Status = time.Now().UnixMilli(), "queued"
	res, err := s.DB.ExecContext(ctx, `insert into deploys(app_id,release_id,trigger,status,log_path,git_sha,git_ref,rebuild,queued_at) values(?,?,?,?,?,?,?,?,?)`,
		d.AppID, d.ReleaseID, d.Trigger, d.Status, d.LogPath, d.GitSHA, d.GitRef, d.Rebuild, d.QueuedAt)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) SetDeployLogPath(ctx context.Context, id int64, path string) error {
	_, err := s.DB.ExecContext(ctx, `update deploys set log_path=? where id=?`, path, id)
	return err
}

func (s *Store) Deploy(ctx context.Context, id int64) (Deploy, error) {
	d, err := scanDeploy(s.DB.QueryRowContext(ctx, `select `+depCols+` from deploys where id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// Deploys lista do mais novo para o mais antigo (before = id exclusivo; 0 = do início).
func (s *Store) Deploys(ctx context.Context, appID int64, limit int, before int64) ([]Deploy, error) {
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := s.DB.QueryContext(ctx, `select `+depCols+` from deploys where app_id=? and id<? order by id desc limit ?`, appID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deploy{}
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClaimNextDeploy pega o deploy mais antigo da fila cujo app não tem outro em execução e o marca running.
func (s *Store) ClaimNextDeploy(ctx context.Context) (*Deploy, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `select d.id from deploys d where d.status='queued'
		and not exists (select 1 from deploys r where r.app_id=d.app_id and r.status='running') order by d.id limit 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `update deploys set status='running', started_at=? where id=? and status='queued'`, time.Now().UnixMilli(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	d, err := scanDeploy(tx.QueryRowContext(ctx, `select `+depCols+` from deploys where id=?`, id))
	if err != nil {
		return nil, err
	}
	return &d, tx.Commit()
}

func (s *Store) SetDeployStage(ctx context.Context, id int64, stage string) error {
	_, err := s.DB.ExecContext(ctx, `update deploys set stage=? where id=?`, stage, id)
	return err
}

func (s *Store) SetDeployRelease(ctx context.Context, id, releaseID int64) error {
	_, err := s.DB.ExecContext(ctx, `update deploys set release_id=? where id=?`, releaseID, id)
	return err
}

func (s *Store) FinishDeploy(ctx context.Context, id int64, status, errMsg string) error {
	var e any
	if errMsg != "" {
		e = errMsg
	}
	_, err := s.DB.ExecContext(ctx, `update deploys set status=?, error=?, finished_at=? where id=?`, status, e, time.Now().UnixMilli(), id)
	return err
}

// CancelQueuedDeploy só cancela quem ainda está na fila; devolve false se já começou.
func (s *Store) CancelQueuedDeploy(ctx context.Context, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `update deploys set status='cancelled', finished_at=? where id=? and status='queued'`, time.Now().UnixMilli(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// FailBuildingReleases limpa releases que ficaram `building` quando o droidd caiu e devolve os caminhos.
func (s *Store) FailBuildingReleases(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `select path from releases where status='building'`)
	if err != nil {
		return nil, err
	}
	var paths []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		paths = append(paths, p)
	}
	rows.Close()
	_, err = s.DB.ExecContext(ctx, `update releases set status='failed' where status='building'`)
	return paths, err
}

// OldDeployLogs devolve (id, log_path) de deploys terminados antes de cutoff, para a retenção de logs.
func (s *Store) OldDeployLogs(ctx context.Context, cutoff time.Time) (map[int64]string, error) {
	rows, err := s.DB.QueryContext(ctx, `select id, log_path from deploys where finished_at is not null and finished_at<? and log_path<>''`, cutoff.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]string{}
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			return nil, err
		}
		m[id] = p
	}
	return m, rows.Err()
}

func (s *Store) ClearDeployLogPath(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `update deploys set log_path='' where id=?`, id)
	return err
}
