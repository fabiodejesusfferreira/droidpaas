#!/usr/bin/env bash
# 2I — túnel (plumbing sem expor o token), Git HTTP de verdade, backup e restauração.
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup

W=$E2E_HOME/work; rm -rf "$W"; mkdir -p "$W/web"
export DROID_CONFIG=$E2E_HOME/droid.toml; D=$E2E_HOME/bin/droid
$D login --url "http://$PANEL" --token "$TOKEN" >/dev/null

say "túnel: o token vai para um envdir 0600 e nunca para o ps"
SECRET=TOKEN-FALSO-PARA-O-TESTE-123456
$D tunnel set --token "$SECRET" >/dev/null
wait_until 15 '[ -d "$E2E_HOME/sv/dpt-cloudflared" ]'
test -d "$E2E_HOME/sv/dpt-cloudflared" && ok "serviço dpt-cloudflared criado" || bad "serviço do túnel ausente"
check "arquivo do token" "$SECRET" "$(cat "$E2E_HOME/home/etc/tunnel-env/TUNNEL_TOKEN")"
check "modo do arquivo do token" 600 "$(stat -c %a "$E2E_HOME/home/etc/tunnel-env/TUNNEL_TOKEN")"
check "modo do diretório do token" 700 "$(stat -c %a "$E2E_HOME/home/etc/tunnel-env")"
case "$(cat "$E2E_HOME/sv/dpt-cloudflared/run")" in *"$SECRET"*) bad "token no script run";; *) ok "script run sem token";; esac
case "$(ps -A -o args)" in *"$SECRET"*) bad "TOKEN VISÍVEL NO PS";; *) ok "token ausente do ps -A";; esac
case "$(sqlite_dump=$(python3 -c "import sqlite3,sys; print(sqlite3.connect('$E2E_HOME/home/data/droidpaas.db').execute(\"select value from settings where key='tunnel_token'\").fetchone()[0])" 2>&1); echo "$sqlite_dump")" in *"$SECRET"*) bad "token em claro no banco";; *) ok "token cifrado no banco";; esac
contains "status mostra o túnel" "túnel" "$($D status)"
$D tunnel set --clear >/dev/null
wait_until 15 '[ ! -d "$E2E_HOME/sv/dpt-cloudflared" ]'
test ! -d "$E2E_HOME/sv/dpt-cloudflared" && ok "serviço removido ao limpar o token" || bad "serviço do túnel continua"
test ! -e "$E2E_HOME/home/etc/tunnel-env/TUNNEL_TOKEN" && ok "arquivo do token apagado" || bad "token ficou em disco"

say "Git HTTP: clone + push com token escopo git, deploy com log no terminal"
$D apps create web >/dev/null
api POST /apps/web/domains '{"hostname":"web.test.dev"}' >/dev/null
api PATCH /apps/web '{"health_path":"/health"}' >/dev/null
GT=$($D tokens create ci --scope git | grep -o 'dp_[A-Za-z0-9_-]*')
URL="http://u:$GT@$PANEL/git/web.git"
nodeapp v1
git -C "$W/web" init -q -b main; git -C "$W/web" remote add origin "$URL"
git -C "$W/web" add -A; git -C "$W/web" -c user.name=e -c user.email=e@e commit -qm v1
OUT=$(GIT_TERMINAL_PROMPT=0 git -C "$W/web" push origin main 2>&1); RC=$?
check "push HTTP saiu 0" 0 "$RC"
contains "build visível no terminal do push HTTP" "remote: DroidPaaS: deploy #1" "$OUT"
contains "deploy concluído" succeeded "$OUT"
wait_until 15 '[ "$(at web.test.dev /)" = v1 ]'
check "app servido" v1 "$(at web.test.dev / | tr -d '\n')"
OUT=$(GIT_TERMINAL_PROMPT=0 git clone -q "http://u:dp_errado@$PANEL/git/web.git" "$W/x" 2>&1); contains "token inválido recusado" "Authentication failed" "$OUT"
check "token git não acessa a API" 403 "$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $GT" "$API/apps")"

say "backup e restauração"
$D env web set SEGREDO=valor-secreto --secret >/dev/null
$D backup --out "$E2E_HOME/b.db" >/dev/null 2>&1
check "backup 0600" 600 "$(stat -c %a "$E2E_HOME/b.db")"
check "apps no backup" web "$(python3 -c "import sqlite3; print(sqlite3.connect('$E2E_HOME/b.db').execute('select name from apps').fetchone()[0])")"
case "$(strings "$E2E_HOME/b.db" 2>/dev/null || python3 -c "print(open('$E2E_HOME/b.db','rb').read().decode('latin1'))")" in *valor-secreto*) bad "segredo em claro no backup";; *) ok "segredo cifrado no backup";; esac
$D backup --out "$E2E_HOME/b.tar.gz" --with-key >/dev/null 2>&1
check "tar contém master.key" master.key "$(tar tzf "$E2E_HOME/b.tar.gz" | grep master.key)"
# restaura o banco do backup num home novo e sobe o droidd nele: os apps e o segredo decifrado voltam
R=$E2E_HOME/restore; rm -rf "$R"; mkdir -p "$R/data" "$R/etc/secrets" "$R/run" "$R/logs/droidd" "$R/apps" "$R/tmp"
tar xzf "$E2E_HOME/b.tar.gz" -C "$R" && mv "$R/droidpaas.db" "$R/data/" && mkdir -p "$R/etc/secrets" && mv "$R/master.key" "$R/etc/secrets/"
sed "s#$E2E_HOME/home#$R#g; s#127.0.0.1:18090#127.0.0.1:18091#; s#127.0.0.1:28080#127.0.0.1:28081#; s#127.0.0.1:22019#127.0.0.1:22020#; s#service_dir = .*#service_dir = '$E2E_HOME/sv-restore'#" "$E2E_HOME/home/etc/droidd.toml" > "$R/etc/droidd.toml"
mkdir -p "$E2E_HOME/sv-restore"
(cd "$R" && nohup "$E2E_HOME/bin/droidd" serve --home "$R" > "$R/droidd.log" 2>&1 </dev/null & echo $! > "$R/pid")
wait_until 15 curl -sf "http://127.0.0.1:18091/healthz"
check "droidd restaurado responde" ok "$(curl -s http://127.0.0.1:18091/healthz | jget status)"
APPS=$(curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:18091/api/v1/apps | python3 -c 'import sys,json; print(",".join(a["name"] for a in json.load(sys.stdin)))')
check "apps restaurados" web "$APPS"
check "segredo decifrado com a master.key do backup" valor-secreto "$(curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:18091/api/v1/apps/web/env/SEGREDO | jget value)"
kill "$(cat "$R/pid")" 2>/dev/null

say "atualização: reiniciar o droidd preserva tudo (migrações idempotentes)"
kill "$(cat "$E2E_HOME/droidd.pid")"; sleep 1; start_droidd && ok "droidd reiniciou" || bad "droidd não subiu"
check "app segue lá" web "$(api GET /apps | jget 0.name)"

e2e_teardown
