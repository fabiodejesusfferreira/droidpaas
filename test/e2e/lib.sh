#!/usr/bin/env bash
# Biblioteca dos roteiros e2e (rodam no Termux). Cria um sandbox isolado em $E2E_HOME, com runsvdir próprio,
# portas e prefixo `dpt-` próprios, sem tocar nos serviços reais do aparelho.
#   uso: source lib.sh; e2e_setup; ...; e2e_teardown
export PATH=/data/data/com.termux/files/usr/bin:$PATH
SRC=${SRC:-$HOME/src-dp}
E2E_HOME=${E2E_HOME:-$HOME/dp-e2e}
PANEL=127.0.0.1:18090
PROXY=127.0.0.1:28080
ADMIN=127.0.0.1:22019
API=http://$PANEL/api/v1
PASS=0; FAILS=0

say()  { printf '\n== %s\n' "$*"; }
ok()   { PASS=$((PASS+1)); printf '  ok   %s\n' "$*"; }
bad()  { FAILS=$((FAILS+1)); printf '  FAIL %s\n' "$*"; }
check() { # check "descrição" esperado obtido
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (esperado [$2], obtido [$3])"; fi
}
contains() { # contains "descrição" trecho texto
  case "$3" in *"$2"*) ok "$1";; *) bad "$1 (não contém [$2]): $(printf %s "$3" | head -c 200)";; esac
}
wait_until() { # wait_until segundos comando...
  local t=$1; shift; local end=$(( $(date +%s) + t ))
  while ! "$@" >/dev/null 2>&1; do [ "$(date +%s)" -ge "$end" ] && return 1; sleep 0.5; done
}
api() { # api METODO caminho [corpo-json]
  local m=$1 p=$2; shift 2
  if [ $# -gt 0 ]; then
    curl -s -m 20 -X "$m" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data "$1" "$API$p"
  else
    curl -s -m 20 -X "$m" -H "Authorization: Bearer $TOKEN" "$API$p"
  fi
}
code() { # code METODO caminho [corpo] -> só o status HTTP
  local m=$1 p=$2; shift 2
  if [ $# -gt 0 ]; then
    curl -s -m 20 -o /dev/null -w '%{http_code}' -X "$m" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data "$1" "$API$p"
  else
    curl -s -m 20 -o /dev/null -w '%{http_code}' -X "$m" -H "Authorization: Bearer $TOKEN" "$API$p"
  fi
}
jget() { python3 -c 'import sys,json; d=json.load(sys.stdin)
for k in sys.argv[1].split("."): d=d[int(k)] if isinstance(d,list) else d[k]
print(d)' "$1" 2>/dev/null; }
svpid() { sv status "$E2E_HOME/sv/$1" 2>/dev/null | grep -o 'pid [0-9]*' | head -1 | cut -d' ' -f2; } # pid do serviço (não do log)
at() { curl -s -m 10 -H "Host: $1" "http://$PROXY$2"; }                 # GET via Caddy com Host
atcode() { curl -s -m 10 -o /dev/null -w '%{http_code}' -H "Host: $1" "http://$PROXY$2"; }

e2e_build() {
  say "build no aparelho"
  mkdir -p "$E2E_HOME"
  (cd "$SRC" && go build -p 2 -o "$E2E_HOME/bin/" ./cmd/...) || { echo "build falhou"; exit 1; }
}

e2e_stop_all() {
  [ -f "$E2E_HOME/droidd.pid" ] && kill "$(cat "$E2E_HOME/droidd.pid")" 2>/dev/null
  if [ -d "$E2E_HOME/sv" ]; then
    for d in "$E2E_HOME"/sv/*/; do
      [ -d "$d" ] || continue
      sv -w 5 exit "${d%/}" >/dev/null 2>&1; [ -d "$d/log" ] && sv -w 5 exit "${d%/}/log" >/dev/null 2>&1
    done
  fi
  [ -f "$E2E_HOME/runsvdir.pid" ] && kill "$(cat "$E2E_HOME/runsvdir.pid")" 2>/dev/null
  pkill -f "droidpaas-e2e" 2>/dev/null
  pkill -f "$E2E_HOME/bin/droidd" 2>/dev/null
  pkill -f "caddy run --config $E2E_HOME" 2>/dev/null
  sleep 1
}

e2e_setup() {
  e2e_stop_all
  rm -rf "$E2E_HOME/home" "$E2E_HOME/sv" "$E2E_HOME"/*.pid "$E2E_HOME"/*.log
  mkdir -p "$E2E_HOME/sv"
  say "init"
  local out
  out=$(printf 'admin\nsenha-de-teste-12345\nsenha-de-teste-12345\n' | "$E2E_HOME/bin/droidd" init --home "$E2E_HOME/home" 2>&1)
  TOKEN=$(printf '%s' "$out" | grep -o 'dp_[A-Za-z0-9_-]*' | head -1)
  [ -n "$TOKEN" ] || { echo "init falhou: $out"; exit 1; }
  local toml="$E2E_HOME/home/etc/droidd.toml"
  sed -i "s#^  PanelAddr.*##; s#panel_addr = .*#panel_addr = '$PANEL'#; s#listen = '127.0.0.1:18080'#listen = '$PROXY'#; s#admin = '127.0.0.1:12019'#admin = '$ADMIN'#; s#service_dir = .*#service_dir = '$E2E_HOME/sv'#; s#prefix = 'dp-'#prefix = 'dpt-'#; s#range_start = .*#range_start = 21000#; s#range_end = .*#range_end = 21099#; s#interval = '10s'#interval = '2s'#" "$toml"
  sed -i "s#ssh_port = .*#ssh_port = 8022#" "$toml"
  # runsvdir próprio (exec -a dá um nome reconhecível para o teardown)
  (exec -a droidpaas-e2e-runsvdir runsvdir "$E2E_HOME/sv" >/dev/null 2>&1 </dev/null &) 
  sleep 1; pgrep -f droidpaas-e2e-runsvdir | head -1 > "$E2E_HOME/runsvdir.pid"
  cd "$E2E_HOME" || exit 1
  nohup "$E2E_HOME/bin/droidd" serve --home "$E2E_HOME/home" > "$E2E_HOME/droidd.log" 2>&1 </dev/null &
  echo $! > "$E2E_HOME/droidd.pid"
  wait_until 20 curl -sf "http://$PANEL/healthz" || { echo "droidd não subiu"; cat "$E2E_HOME/droidd.log"; exit 1; }
  ok "sandbox pronto (token ${TOKEN:0:9}…)"
}

e2e_teardown() {
  e2e_stop_all
  say "resultado: $PASS ok, $FAILS falhas"
  [ "$FAILS" -eq 0 ]
}
