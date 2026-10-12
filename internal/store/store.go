// Package store abre o SQLite (WAL) e aplica as migrações embutidas (TSD §7, ADR-09).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"droidpaas/migrations"
)

type Store struct{ DB *sql.DB }

// Open abre (ou cria) o banco e aplica as migrações pendentes.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// shortcut: poucas conexões; WAL permite leitores concorrentes, escrita é serializada pelo SQLite.
	db.SetMaxOpenConns(4)
	if err := migrate(ctx, db, migrations.FS, path); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func migrate(ctx context.Context, db *sql.DB, fsys fs.FS, path string) error {
	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	applied := map[int]bool{}
	var exists int
	if err := db.QueryRowContext(ctx, `select count(*) from sqlite_master where name='schema_migrations'`).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		rows, err := db.QueryContext(ctx, `select version from schema_migrations`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			applied[v] = true
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}

	backedUp := false
	for _, f := range files {
		v, err := strconv.Atoi(strings.SplitN(f, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migração %q: nome deve começar com número", f)
		}
		if applied[v] {
			continue
		}
		// Backup antes de migrar um banco que já tem dados (R-10, §14.3).
		if !backedUp && len(applied) > 0 {
			bak := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102T150405Z"))
			if _, err := db.ExecContext(ctx, "vacuum into ?", bak); err != nil {
				return fmt.Errorf("backup antes de migrar: %w", err)
			}
			os.Chmod(bak, 0o600)
			backedUp = true
		}
		body, err := fs.ReadFile(fsys, f)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migração %s: %w", f, err)
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version, applied_at) values(?, ?)`, v, time.Now().UnixMilli()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		applied[v] = true
	}
	return nil
}

// InterruptRunningDeploys marca como interrupted os deploys que estavam running quando o
// droidd caiu (TSD §8.1) e devolve quantos eram e quantos seguem na fila.
func (s *Store) InterruptRunningDeploys(ctx context.Context) (interrupted, queued int64, err error) {
	res, err := s.DB.ExecContext(ctx, `update deploys set status='interrupted', finished_at=? where status='running'`, time.Now().UnixMilli())
	if err != nil {
		return 0, 0, err
	}
	interrupted, _ = res.RowsAffected()
	err = s.DB.QueryRowContext(ctx, `select count(*) from deploys where status='queued'`).Scan(&queued)
	return
}

var ErrNotFound = errors.New("não encontrado")

func (s *Store) CountUsers(ctx context.Context) (n int, err error) {
	err = s.DB.QueryRowContext(ctx, `select count(*) from users`).Scan(&n)
	return
}

func (s *Store) CreateUser(ctx context.Context, name, hash string) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := s.DB.ExecContext(ctx, `insert into users(username, password_hash, created_at, updated_at) values(?,?,?,?)`, name, hash, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) CreateToken(ctx context.Context, userID int64, name, hash, prefix string) error {
	_, err := s.DB.ExecContext(ctx, `insert into api_tokens(user_id, name, token_hash, prefix, created_at) values(?,?,?,?,?)`,
		userID, name, hash, prefix, time.Now().UnixMilli())
	return err
}
