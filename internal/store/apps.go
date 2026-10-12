package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrExists = errors.New("já existe")

type App struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Runtime          string  `json:"runtime"`
	Port             *int    `json:"port"`
	Branch           string  `json:"branch"`
	BuildCmd         *string `json:"build_cmd"`
	StartCmd         *string `json:"start_cmd"`
	HealthPath       *string `json:"health_path"`
	HealthTimeoutS   int     `json:"health_timeout_s"`
	StaticDir        *string `json:"static_dir"`
	Nice             int     `json:"nice"`
	DesiredState     string  `json:"desired_state"`
	CurrentReleaseID *int64  `json:"current_release_id"`
	CrashedAt        *int64  `json:"crashed_at"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

const appCols = `id,name,runtime,port,branch,build_cmd,start_cmd,health_path,health_timeout_s,static_dir,nice,desired_state,current_release_id,crashed_at,created_at,updated_at`

func scanApp(sc interface{ Scan(...any) error }) (App, error) {
	var a App
	err := sc.Scan(&a.ID, &a.Name, &a.Runtime, &a.Port, &a.Branch, &a.BuildCmd, &a.StartCmd, &a.HealthPath, &a.HealthTimeoutS,
		&a.StaticDir, &a.Nice, &a.DesiredState, &a.CurrentReleaseID, &a.CrashedAt, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Store) InsertApp(ctx context.Context, a *App) error {
	now := time.Now().UnixMilli()
	a.CreatedAt, a.UpdatedAt = now, now
	res, err := s.DB.ExecContext(ctx, `insert into apps(name,runtime,port,branch,build_cmd,start_cmd,health_path,health_timeout_s,static_dir,nice,desired_state,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.Name, a.Runtime, a.Port, a.Branch, a.BuildCmd, a.StartCmd, a.HealthPath, a.HealthTimeoutS, a.StaticDir, a.Nice, a.DesiredState, now, now)
	if isUnique(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) AppByName(ctx context.Context, name string) (App, error) {
	a, err := scanApp(s.DB.QueryRowContext(ctx, `select `+appCols+` from apps where name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) AppByID(ctx context.Context, id int64) (App, error) {
	a, err := scanApp(s.DB.QueryRowContext(ctx, `select `+appCols+` from apps where id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) ListApps(ctx context.Context) ([]App, error) {
	rows, err := s.DB.QueryContext(ctx, `select `+appCols+` from apps order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateApp grava os campos editáveis (não mexe em estado desejado, release atual nem port).
func (s *Store) UpdateApp(ctx context.Context, a App) error {
	_, err := s.DB.ExecContext(ctx, `update apps set runtime=?,branch=?,build_cmd=?,start_cmd=?,health_path=?,health_timeout_s=?,static_dir=?,nice=?,updated_at=? where id=?`,
		a.Runtime, a.Branch, a.BuildCmd, a.StartCmd, a.HealthPath, a.HealthTimeoutS, a.StaticDir, a.Nice, time.Now().UnixMilli(), a.ID)
	return err
}

func (s *Store) SetDesiredState(ctx context.Context, id int64, state string) error {
	_, err := s.DB.ExecContext(ctx, `update apps set desired_state=?,updated_at=? where id=?`, state, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) SetCurrentRelease(ctx context.Context, id int64, releaseID *int64) error {
	_, err := s.DB.ExecContext(ctx, `update apps set current_release_id=?,updated_at=? where id=?`, releaseID, time.Now().UnixMilli(), id)
	return err
}

// SetCrashed marca (ts) ou limpa (nil) o crash loop.
func (s *Store) SetCrashed(ctx context.Context, id int64, ts *int64) error {
	_, err := s.DB.ExecContext(ctx, `update apps set crashed_at=? where id=?`, ts, id)
	return err
}

func (s *Store) DeleteApp(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `delete from apps where id=?`, id)
	return err
}

func (s *Store) UsedPorts(ctx context.Context) (map[int]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `select port from apps where port is not null`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		m[p] = true
	}
	return m, rows.Err()
}

// ---- domínios ----

type Domain struct {
	ID        int64  `json:"id"`
	AppID     int64  `json:"app_id"`
	Hostname  string `json:"hostname"`
	IsPrimary bool   `json:"is_primary"`
	Auto      bool   `json:"auto"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) Domains(ctx context.Context, appID int64) ([]Domain, error) {
	q := `select id,app_id,hostname,is_primary,auto,created_at from domains`
	args := []any{}
	if appID > 0 {
		q += ` where app_id=?`
		args = append(args, appID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` order by auto desc, hostname`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.AppID, &d.Hostname, &d.IsPrimary, &d.Auto, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) AddDomain(ctx context.Context, appID int64, host string, auto, primary bool) error {
	_, err := s.DB.ExecContext(ctx, `insert into domains(app_id,hostname,is_primary,auto,created_at) values(?,?,?,?,?)`, appID, host, primary, auto, time.Now().UnixMilli())
	if isUnique(err) {
		return ErrExists
	}
	return err
}

func (s *Store) RemoveDomain(ctx context.Context, appID int64, host string) error {
	res, err := s.DB.ExecContext(ctx, `delete from domains where app_id=? and hostname=?`, appID, host)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- variáveis de ambiente ----

type EnvVar struct {
	Key       string
	ValueEnc  []byte
	IsSecret  bool
	UpdatedAt int64
}

func (s *Store) Env(ctx context.Context, appID int64) ([]EnvVar, error) {
	rows, err := s.DB.QueryContext(ctx, `select key,value_enc,is_secret,updated_at from env_vars where app_id=? order by key`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnvVar{}
	for rows.Next() {
		var e EnvVar
		if err := rows.Scan(&e.Key, &e.ValueEnc, &e.IsSecret, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ApplyEnv grava e remove variáveis numa única transação.
func (s *Store) ApplyEnv(ctx context.Context, appID int64, set []EnvVar, unset []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	for _, e := range set {
		if _, err := tx.ExecContext(ctx, `insert into env_vars(app_id,key,value_enc,is_secret,updated_at) values(?,?,?,?,?)
			on conflict(app_id,key) do update set value_enc=excluded.value_enc,is_secret=excluded.is_secret,updated_at=excluded.updated_at`,
			appID, e.Key, e.ValueEnc, e.IsSecret, now); err != nil {
			return err
		}
	}
	for _, k := range unset {
		if _, err := tx.ExecContext(ctx, `delete from env_vars where app_id=? and key=?`, appID, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}
