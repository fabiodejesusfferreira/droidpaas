// Package crypt cifra segredos em repouso com AES-256-GCM e a master.key (TSD §8.8).
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master.key deve ter 32 bytes, tem %d", len(key))
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Box{g}, nil
}

func Load(path string) (*Box, error) {
	k, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return New(k)
}

// Seal devolve nonce||ciphertext, com nonce aleatório por valor.
func (b *Box) Seal(plain []byte) ([]byte, error) {
	n := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return b.aead.Seal(n, n, plain, nil), nil
}

func (b *Box) Open(sealed []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(sealed) < ns+b.aead.Overhead() {
		return nil, errors.New("valor cifrado truncado")
	}
	return b.aead.Open(nil, sealed[:ns], sealed[ns:], nil)
}
