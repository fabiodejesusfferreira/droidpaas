package runit

import (
	"fmt"
	"os"
	"strings"
)

// Prefix devolve $PREFIX do Termux (ou o caminho padrão).
func Prefix() string {
	if p := os.Getenv("PREFIX"); p != "" {
		return p
	}
	return "/data/data/com.termux/files/usr"
}

// AppRun gera o script run do app (TSD A.1): exec substitui o shell para os sinais chegarem ao app.
// `chpst -n` não tem efeito no Android 13 (PoC V-09) mas é inofensivo. O chpst não executa scripts com
// shebang no Termux ("file does not exist", achado da 2C), então o start.sh roda via `sh`.
func AppRun(prefix, appDir string, nice int) string {
	return fmt.Sprintf(`#!%[1]s/bin/sh
exec 2>&1
export PATH=%[1]s/bin:$PATH
cd %[2]s/current || exit 1
exec chpst -e %[2]s/env -n %[3]d sh %[2]s/start.sh
`, prefix, shq(appDir), nice)
}

// LogRun gera o log/run (TSD A.2).
func LogRun(prefix, logDir string) string {
	return fmt.Sprintf("#!%s/bin/sh\nexec svlogd -tt %s\n", prefix, shq(logDir))
}

// LogConfig é o config do svlogd: 1 MB × 5 arquivos por padrão.
func LogConfig(sizeBytes, files int) string { return fmt.Sprintf("s%d\nn%d\n", sizeBytes, files) }

// StartScript gera o start.sh: o comando do app roda com exec; venv do Python entra no PATH.
func StartScript(prefix, cmd string, venv bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#!%s/bin/sh\n", prefix)
	if venv {
		b.WriteString("PATH=\"$PWD/.venv/bin:$PATH\"\nexport PATH\n")
	}
	// shortcut: comandos compostos ou com VAR=x ficam como filho do shell (sem exec), então o SIGTERM
	// pode não chegar ao app; trocar por supervisor de grupo de processos se isso incomodar.
	if needsShell(cmd) {
		fmt.Fprintf(&b, "%s\n", cmd)
	} else {
		fmt.Fprintf(&b, "exec %s\n", cmd)
	}
	return b.String()
}

func needsShell(cmd string) bool {
	first, _, _ := strings.Cut(strings.TrimSpace(cmd), " ")
	return strings.ContainsAny(cmd, "&|;<>(){}$`") || strings.Contains(first, "=")
}

// shq cita um caminho para o shell (aspas simples).
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
