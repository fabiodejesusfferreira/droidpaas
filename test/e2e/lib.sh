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
SSH_PORT=28022
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
wait_until() { # wait_until segundos 'expressão' | wait_until segundos comando args...
  # com uma expressão entre aspas simples ela é reavaliada a cada tentativa (um "$(…)" sem aspas simples seria avaliado só uma vez)
  local t=$1; shift; local end=$(( $(date +%s) + t ))
  while true; do
    if [ $# -eq 1 ]; then eval "$1" >/dev/null 2>&1 && return 0; else "$@" >/dev/null 2>&1 && return 0; fi
    [ "$(date +%s)" -ge "$end" ] && return 1; sleep 0.5
  done
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


# ---- helpers de deploy (usam $W = diretório de trabalho do teste) ----
gitc() { git -C "$1" -c user.name=e2e -c user.email=e2e@e2e "${@:2}"; }
newapp() { # newapp nome dir  → cria o app pela API, o repo bare e o remote local
  api POST /apps "{\"name\":\"$1\"}" >/dev/null
  git init -q --bare -b main "$E2E_HOME/home/apps/$1/repo.git" 2>/dev/null
  rm -f "$E2E_HOME/home/apps/$1/repo.git/hooks/post-receive"   # estes testes disparam os deploys pela API; o hook é testado na 2F
  gitc "$2" init -q -b main; gitc "$2" remote add origin "$E2E_HOME/home/apps/$1/repo.git"
}
publish() { gitc "$1" add -A; gitc "$1" commit -qm "$2" --allow-empty; gitc "$1" push -q origin main; }
deploy() { # deploy app [json] → imprime "id status"
  local id s; id=$(api POST "/apps/$1/deploys" "${2:-{\}}" | jget id)
  for _ in $(seq 1 240); do
    s=$(api GET "/deploys/$id" | jget status)
    case "$s" in queued|running) sleep 0.5;; *) echo "$id $s"; return;; esac
  done
  echo "$id timeout"
}
nodeapp() { # nodeapp versão → escreve o app de teste em $W/web
  mkdir -p "$W/web"
  cat > "$W/web/server.js" <<JS
const http = require("http");
http.createServer((q, r) => {
  console.log(q.method, q.url);
  if (q.url === "/health") return r.end("ok");
  if (q.url.startsWith("/env/")) return r.end(String(process.env[q.url.slice(5)]));
  r.end("$1\n");
}).listen(process.env.PORT, process.env.HOST, () => console.log("listening on " + process.env.PORT));
JS
  echo '{"name":"web","version":"1.0.0","scripts":{"start":"node server.js"}}' > "$W/web/package.json"
}
start_droidd() {
  cd "$E2E_HOME" || exit 1
  nohup "$E2E_HOME/bin/droidd" serve --home "$E2E_HOME/home" >> "$E2E_HOME/droidd.log" 2>&1 </dev/null &
  echo $! > "$E2E_HOME/droidd.pid"
  wait_until 20 curl -sf "http://$PANEL/healthz"
}

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
      sv -w 8 down "${d%/}" >/dev/null 2>&1   # `exit` sozinho deixa o app rodando (órfão segurando a porta)
      sv -w 5 exit "${d%/}" >/dev/null 2>&1; [ -d "$d/log" ] && sv -w 5 exit "${d%/}/log" >/dev/null 2>&1
    done
  fi
  [ -f "$E2E_HOME/runsvdir.pid" ] && kill "$(cat "$E2E_HOME/runsvdir.pid")" 2>/dev/null
  [ -f "$E2E_HOME/ssh/sshd.pid" ] && kill "$(cat "$E2E_HOME/ssh/sshd.pid")" 2>/dev/null
  pkill -f "droidpaas-e2e" 2>/dev/null
  pkill -f "$E2E_HOME/bin/droidd" 2>/dev/null
  pkill -f "caddy run --config $E2E_HOME" 2>/dev/null
  # processos de apps de execuções antigas (cwd dentro do sandbox) não podem sobreviver: seguram portas e mascaram falhas
  for p in $(pgrep -u "$(id -u)" -x node 2>/dev/null); do
    case "$(readlink "/proc/$p/cwd" 2>/dev/null)" in "$E2E_HOME"/*) kill -9 "$p" 2>/dev/null;; esac
  done
  sleep 1
}

e2e_setup() {
  e2e_stop_all
  rm -rf "$E2E_HOME/home" "$E2E_HOME/sv" "$E2E_HOME"/*.pid "$E2E_HOME"/*.log "$E2E_HOME/authorized_keys"
  mkdir -p "$E2E_HOME/sv"
  say "init"
  local out
  out=$(printf 'admin\nsenha-de-teste-12345\nsenha-de-teste-12345\n' | "$E2E_HOME/bin/droidd" init --home "$E2E_HOME/home" 2>&1)
  TOKEN=$(printf '%s' "$out" | grep -o 'dp_[A-Za-z0-9_-]*' | head -1)
  [ -n "$TOKEN" ] || { echo "init falhou: $out"; exit 1; }
  local toml="$E2E_HOME/home/etc/droidd.toml"
  sed -i "s#^  PanelAddr.*##; s#panel_addr = .*#panel_addr = '$PANEL'#; s#listen = '127.0.0.1:18080'#listen = '$PROXY'#; s#admin = '127.0.0.1:12019'#admin = '$ADMIN'#; s#service_dir = .*#service_dir = '$E2E_HOME/sv'#; s#prefix = 'dp-'#prefix = 'dpt-'#; s#range_start = .*#range_start = 21000#; s#range_end = .*#range_end = 21099#; s#interval = '10s'#interval = '2s'#" "$toml"
  sed -i "s#ssh_port = .*#ssh_port = $SSH_PORT#; s#authorized_keys = .*#authorized_keys = '$E2E_HOME/authorized_keys'#" "$toml"
  # runsvdir próprio (exec -a dá um nome reconhecível para o teardown)
  (exec -a droidpaas-e2e-runsvdir runsvdir "$E2E_HOME/sv" >/dev/null 2>&1 </dev/null &) 
  sleep 1; pgrep -f droidpaas-e2e-runsvdir | head -1 > "$E2E_HOME/runsvdir.pid"
  : > "$E2E_HOME/droidd.log"
  start_droidd || { echo "droidd não subiu"; cat "$E2E_HOME/droidd.log"; exit 1; }
  ok "sandbox pronto (token ${TOKEN:0:9}…)"
}

# sshd próprio do sandbox (chaves, authorized_keys e porta próprios; não toca no sshd real do aparelho)
e2e_sshd() {
  local d=$E2E_HOME/ssh; rm -rf "$d"; mkdir -p "$d"; chmod 700 "$d"
  ssh-keygen -q -t ed25519 -N '' -f "$d/host" && ssh-keygen -q -t ed25519 -N '' -f "$d/client" -C e2e-client
  cat > "$d/sshd_config" <<CFG
Port $SSH_PORT
ListenAddress 127.0.0.1
HostKey $d/host
AuthorizedKeysFile $E2E_HOME/authorized_keys
PidFile $d/sshd.pid
PasswordAuthentication no
KbdInteractiveAuthentication no
StrictModes no
CFG
  /data/data/com.termux/files/usr/bin/sshd -f "$d/sshd_config" -E "$d/sshd.log"
  wait_until 10 test -f "$d/sshd.pid" || { echo "sshd do sandbox não subiu"; cat "$d/sshd.log"; exit 1; }
  SSH_USER=$(whoami)
  GIT_SSH_COMMAND="ssh -i $d/client -p $SSH_PORT -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"
  export GIT_SSH_COMMAND SSH_USER
}

e2e_teardown() {
  e2e_stop_all
  say "resultado: $PASS ok, $FAILS falhas"
  [ "$FAILS" -eq 0 ]
}
