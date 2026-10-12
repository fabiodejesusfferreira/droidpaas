package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.db")
	s, err := Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}

func TestOpenAppliesSchemaAndPragmas(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	var n int
	s.DB.QueryRowContext(ctx, `select count(*) from sqlite_master where type='table' and name not like 'sqlite_%'`).Scan(&n)
	if n != 12 {
		t.Fatalf("tabelas = %d, esperado 12", n)
	}
	var mode string
	var fk int
	s.DB.QueryRowContext(ctx, `pragma journal_mode`).Scan(&mode)
	s.DB.QueryRowContext(ctx, `pragma foreign_keys`).Scan(&fk)
	if mode != "wal" || fk != 1 {
		t.Fatalf("pragmas: %s fk=%d", mode, fk)
	}
}

func TestReopenIsIdempotentAndNoBackup(t *testing.T) {
	s, p := open(t)
	s.Close()
	s2, err := Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if m, _ := filepath.Glob(p + ".bak-*"); len(m) != 0 {
		t.Fatalf("backup indevido: %v", m)
	}
}

func TestMigrateBacksUpBeforeNewMigration(t *testing.T) {
	s, p := open(t)
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "a", "h"); err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"0001_init.sql": {Data: []byte(`select 1;`)}, // já aplicadas: ignoradas
		"0002_y.sql":    {Data: []byte(`select 1;`)},
		"0003_x.sql":    {Data: []byte(`create table extra(id integer);`)},
	}
	if err := migrate(ctx, s.DB, fsys, p); err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(p + ".bak-*"); len(m) != 1 {
		t.Fatalf("esperava 1 backup, achei %v", m)
	} else if st, _ := os.Stat(m[0]); st.Mode().Perm() != 0o600 {
		t.Fatalf("permissão do backup: %v", st.Mode())
	}
	var n int
	s.DB.QueryRowContext(ctx, `select count(*) from schema_migrations`).Scan(&n)
	if n != 3 {
		t.Fatalf("migrações aplicadas = %d", n)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	s, p := open(t)
	fsys := fstest.MapFS{"0003_bad.sql": {Data: []byte(`create table ok(id integer); select * from nao_existe;`)}}
	if err := migrate(context.Background(), s.DB, fsys, p); err == nil {
		t.Fatal("esperava erro")
	}
	var n int
	s.DB.QueryRow(`select count(*) from sqlite_master where name='ok'`).Scan(&n)
	if n != 0 {
		t.Fatal("migração falha não foi desfeita")
	}
}

func TestInterruptRunningDeploys(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	s.DB.Exec(`insert into apps(name, created_at, updated_at) values('a',1,1)`)
	for _, st := range []string{"running", "queued", "succeeded"} {
		s.DB.Exec(`insert into deploys(app_id, trigger, status, log_path, queued_at) values(1,'manual',?, '/l', 1)`, st)
	}
	i, q, err := s.InterruptRunningDeploys(ctx)
	if err != nil || i != 1 || q != 1 {
		t.Fatalf("i=%d q=%d err=%v", i, q, err)
	}
}

func TestCascadeOnAppDelete(t *testing.T) {
	s, _ := open(t)
	s.DB.Exec(`insert into apps(name, created_at, updated_at) values('a',1,1)`)
	s.DB.Exec(`insert into env_vars(app_id,key,value_enc,updated_at) values(1,'K','x',1)`)
	s.DB.Exec(`delete from apps where id=1`)
	var n int
	s.DB.QueryRow(`select count(*) from env_vars`).Scan(&n)
	if n != 0 {
		t.Fatal("ON DELETE CASCADE não funcionou (foreign_keys desligado?)")
	}
}
