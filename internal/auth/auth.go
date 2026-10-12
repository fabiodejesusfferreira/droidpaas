// Package auth: hash de senha (argon2id) e tokens de API (TSD §13.2).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const MinPasswordLen = 12

// Parâmetros moderados (OWASP: 19 MiB, t=2). Validar ≤ ~500 ms no M22 (TSD §13.2).
const (
	argonTime    = 2
	argonMemKiB  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func HashPassword(pw string) (string, error) {
	if len([]rune(pw)) < MinPasswordLen {
		return "", fmt.Errorf("a senha precisa ter ao menos %d caracteres", MinPasswordLen)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(pw, phc string) bool {
	p := strings.Split(phc, "$") // "", argon2id, v=, m=,t=,p=, salt, key
	if len(p) != 6 || p[1] != "argon2id" {
		return false
	}
	var m uint32
	var t uint32
	var th uint8
	if _, err := fmt.Sscanf(p[3], "m=%d,t=%d,p=%d", &m, &t, &th); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(p[4])
	want, err2 := base64.RawStdEncoding.DecodeString(p[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, th, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken devolve o token ("dp_" + 32 bytes base64url), seu hash e o prefixo exibível.
func NewToken() (token, hash, prefix string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	token = "dp_" + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), token[:9], nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

var ErrBadToken = errors.New("token inválido")

// TokenMatches compara em tempo constante o token apresentado com o hash guardado.
func TokenMatches(token, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(storedHash)) == 1
}
