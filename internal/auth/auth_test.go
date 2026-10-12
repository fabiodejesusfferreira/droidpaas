package auth

import (
	"strings"
	"testing"
)

func TestPassword(t *testing.T) {
	if _, err := HashPassword("curta"); err == nil {
		t.Fatal("senha curta deveria falhar")
	}
	h, err := HashPassword("uma-senha-bem-longa")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatal(h)
	}
	if !VerifyPassword("uma-senha-bem-longa", h) || VerifyPassword("outra-senha-longa!", h) {
		t.Fatal("verificação incorreta")
	}
	for _, bad := range []string{"", "x", "$argon2id$v=19$m=1,t=1,p=1$@@$@@"} {
		if VerifyPassword("uma-senha-bem-longa", bad) {
			t.Fatalf("hash inválido aceito: %q", bad)
		}
	}
}

func TestToken(t *testing.T) {
	tok, hash, prefix, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "dp_") || !strings.HasPrefix(tok, prefix) || len(prefix) != 9 {
		t.Fatalf("formato: %q %q", tok, prefix)
	}
	if !TokenMatches(tok, hash) || TokenMatches(tok+"x", hash) {
		t.Fatal("comparação incorreta")
	}
	if strings.Contains(hash, tok) {
		t.Fatal("hash não pode conter o token")
	}
}
