//go:build termux

package proxy

import (
	"os/exec"
	"testing"
)

// Valida o golden file com o Caddy real do aparelho.
func TestGoldenValidatesWithRealCaddy(t *testing.T) {
	out, err := exec.Command("caddy", "validate", "--config", "testdata/golden.json").CombinedOutput()
	if err != nil {
		t.Fatalf("caddy validate: %v\n%s", err, out)
	}
}
