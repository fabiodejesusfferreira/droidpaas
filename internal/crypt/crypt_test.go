package crypt

import (
	"bytes"
	"testing"
)

func TestSealOpen(t *testing.T) {
	b, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := b.Seal([]byte("segredo"))
	c, _ := b.Seal([]byte("segredo"))
	if bytes.Equal(a, c) {
		t.Fatal("nonce deve ser aleatório por valor")
	}
	got, err := b.Open(a)
	if err != nil || string(got) != "segredo" {
		t.Fatalf("%q %v", got, err)
	}
	a[len(a)-1] ^= 1
	if _, err := b.Open(a); err == nil {
		t.Fatal("adulteração não detectada")
	}
	if _, err := b.Open([]byte("x")); err == nil {
		t.Fatal("truncado deveria falhar")
	}
	if _, err := New([]byte("curta")); err == nil {
		t.Fatal("chave curta deveria falhar")
	}
}
