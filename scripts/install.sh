#!/data/data/com.termux/files/usr/bin/sh
# Instala o DroidPaaS no Termux a partir deste checkout: confere os pacotes, compila no aparelho (P-05) e roda `droidd init`.
# Uso: sh scripts/install.sh [--home DIR]      (padrão: ~/droidpaas)
set -eu

HOME_DIR="${HOME}/droidpaas"
while [ $# -gt 0 ]; do
  case "$1" in
    --home) HOME_DIR="$2"; shift 2 ;;
    -h|--help) sed -n '2,3p' "$0"; exit 0 ;;
    *) echo "argumento desconhecido: $1" >&2; exit 2 ;;
  esac
done

[ -n "${PREFIX:-}" ] && [ -d "$PREFIX" ] || { echo "Este instalador é para o Termux (PREFIX não definido)." >&2; exit 1; }
cd "$(dirname "$0")/.."

say() { printf '\n==> %s\n' "$*"; }

say "Pacotes"
REQUIRED="git openssh runit termux-services caddy golang"
OPTIONAL="nodejs python cloudflared termux-api jq"
missing=""
for p in $REQUIRED; do
  dpkg -s "$p" >/dev/null 2>&1 || missing="$missing $p"
done
if [ -n "$missing" ]; then
  echo "Faltam pacotes obrigatórios:$missing"
  printf 'Instalar agora com `pkg install`? [s/N] '
  read -r ans
  case "$ans" in s|S|y|Y) pkg install -y $missing ;; *) echo "Instale e rode de novo."; exit 1 ;; esac
fi
for p in $OPTIONAL; do
  dpkg -s "$p" >/dev/null 2>&1 || echo "  dica: pkg install $p  (necessário para os runtimes/túnel/sensores que você for usar)"
done

say "Go"
go version
need="1.26"
have=$(go env GOVERSION | sed 's/^go//')
[ "$(printf '%s\n%s\n' "$need" "$have" | sort -V | head -n1)" = "$need" ] || { echo "Go $have é antigo demais (preciso de >= $need)." >&2; exit 1; }

say "Compilando no aparelho"
mkdir -p "$HOME_DIR/bin"
go build -ldflags "-X main.version=$(git describe --tags --always 2>/dev/null || echo 0.1.0-dev)" -o "$HOME_DIR/bin/" ./cmd/...
ls -l "$HOME_DIR/bin"

say "droidd init"
"$HOME_DIR/bin/droidd" init --home "$HOME_DIR"

cat <<MSG

Pronto. Próximos passos:
  1. Reabra o Termux (para o runsvdir do termux-services subir) se o init avisou que o serviço não foi criado.
  2. $HOME_DIR/bin/droid login --url http://127.0.0.1:8090 --token <token exibido acima>
  3. Leia docs/operacao.md (phantom killer, bateria, backup).
MSG
