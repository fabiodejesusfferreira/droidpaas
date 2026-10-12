package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"droidpaas/internal/auth"
	"droidpaas/internal/config"
	"droidpaas/internal/store"
)

func TestInitEndToEnd(t *testing.T) { // AT-01
	home := filepath.Join(t.TempDir(), "dp")
	in := "admin\numa-senha-bem-longa\numa-senha-bem-longa\n"
	var out, errw bytes.Buffer
	if err := runInit([]string{"--home", home}, strings.NewReader(in), &out, &errw); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	m := regexp.MustCompile(`dp_[A-Za-z0-9_-]{43}`).FindString(out.String())
	if m == "" {
		t.Fatalf("token não exibido:\n%s", out.String())
	}
	for _, f := range []string{"etc/secrets/master.key", "etc/droidd.toml", "data/droidpaas.db"} {
		st, err := os.Stat(filepath.Join(home, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, err, st)
		}
	}
	if st, _ := os.Stat(filepath.Join(home, "etc", "secrets")); st.Mode().Perm() != 0o700 {
		t.Errorf("etc/secrets: %v", st.Mode())
	}
	if b, _ := os.ReadFile(filepath.Join(home, "etc", "secrets", "master.key")); len(b) != 32 {
		t.Errorf("master.key tem %d bytes", len(b))
	}

	cfg, err := config.Load(filepath.Join(home, "etc", "droidd.toml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var hash, tokHash string
	st.DB.QueryRow(`select password_hash from users where username='admin'`).Scan(&hash)
	st.DB.QueryRow(`select token_hash from api_tokens`).Scan(&tokHash)
	if !auth.VerifyPassword("uma-senha-bem-longa", hash) || !auth.TokenMatches(m, tokHash) {
		t.Fatal("senha/token gravados não conferem")
	}

	// segunda execução é idempotente: não recria admin nem token
	out.Reset()
	if err := runInit([]string{"--home", home}, strings.NewReader(""), &out, &errw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "dp_") || !strings.Contains(out.String(), "já existe") {
		t.Fatalf("init repetido não foi idempotente:\n%s", out.String())
	}
}

func TestInitRejectsShortAndMismatchedPassword(t *testing.T) {
	for _, in := range []string{"admin\ncurta\ncurta\n", "admin\numa-senha-bem-longa\noutra-senha-longa\n"} {
		home := filepath.Join(t.TempDir(), "dp")
		var out, errw bytes.Buffer
		if err := runInit([]string{"--home", home}, strings.NewReader(in), &out, &errw); err == nil {
			t.Errorf("esperava erro para %q", in)
		}
	}
}
