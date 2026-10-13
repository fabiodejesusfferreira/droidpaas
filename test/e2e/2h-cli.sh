#!/usr/bin/env bash
# 2H — runtimes (node com build, python, go, static, custom) e a CLI `droid` de ponta a ponta (AT-07 via CLI).
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup

W=$E2E_HOME/work; rm -rf "$W"; mkdir -p "$W"
export DROID_CONFIG=$E2E_HOME/droid.toml; rm -f "$DROID_CONFIG"
D=$E2E_HOME/bin/droid
FX=$SRC/test/fixtures

say "droid login"
OUT=$($D login --url "http://$PANEL" --token "$TOKEN" 2>&1); contains "login" "autenticado como admin" "$OUT"
check "config 0600" 600 "$(stat -c %a "$DROID_CONFIG")"
OUT=$($D apps 2>&1); contains "droid apps vazio" APP "$OUT"

fixture() { # fixture app dir esperado [segundos]
  local app=$1 dir=$2 want=$3
  newapp "$app" "$dir"; publish "$dir" "$app"
  $D domains "$app" add "$app.test.dev" >/dev/null
  local t0=$(date +%s)
  OUT=$($D deploy "$app" 2>&1); RC=$?
  echo "  deploy $app: $(( $(date +%s) - t0 )) s"
  check "droid deploy $app saiu 0" 0 "$RC"
  contains "log do deploy no terminal ($app)" "-----> finalize" "$OUT"
  wait_until 20 "[ \"\$(at $app.test.dev /)\" != \"\" ] && [ \"\$(atcode $app.test.dev /)\" = 200 ]"
  contains "$app responde" "$want" "$(at "$app.test.dev" /)"
}

say "node com build (npm run build → dist)"
cp -r "$FX/node-build" "$W/nodeb"; fixture nodeb "$W/nodeb" "hello from built fixture"
test -f "$E2E_HOME/home/apps/nodeb/current/dist/server.js" && ok "dist/ gerado pelo build" || bad "dist/ ausente"
contains "start direto com node (sem npm intermediário)" "exec node dist/server.js" "$(cat "$E2E_HOME/home/apps/nodeb/start.sh")"

say "python (venv + pip + Procfile)"
cp -r "$FX/python" "$W/py"; fixture py "$W/py" "hello from python fixture"
contains "venv no PATH do start.sh" '.venv/bin' "$(cat "$E2E_HOME/home/apps/py/start.sh")"

say "go (go build)"
cp -r "$FX/go" "$W/goapp"; fixture goapp "$W/goapp" "hello from go fixture"
test -x "$E2E_HOME/home/apps/goapp/current/app" && ok "binário ./app gerado" || bad "binário ausente"

say "static (sem processo)"
cp -r "$FX/static" "$W/site"; fixture site "$W/site" "hello from static fixture"
check "site sem processo" down "$(sv status "$E2E_HOME/sv/dpt-app-site" | cut -d: -f1)"

say "custom (Procfile + start com shell)"
mkdir -p "$W/cust"; echo "hello custom" > "$W/cust/index.html"
printf 'web: python -m http.server "$PORT" --bind 127.0.0.1\n' > "$W/cust/Procfile"
fixture cust "$W/cust" "hello custom"

say "runtime sem evidência → erro claro"
mkdir -p "$W/vazio"; echo x > "$W/vazio/README"
newapp vazio "$W/vazio"; publish "$W/vazio" x
OUT=$($D deploy vazio 2>&1); RC=$?
check "deploy sem runtime sai 1" 1 "$RC"; contains "mensagem de detecção" "detectar o runtime" "$OUT"

say "CLI: apps, status, deploys, env, logs, rollback, rm"
OUT=$($D apps 2>&1); contains "apps lista nodeb" nodeb "$OUT"; contains "apps lista py" py "$OUT"
OUT=$($D status 2>&1); contains "status mostra o Caddy" dpt-caddy "$OUT"; echo "$OUT" | sed "s/^/    /"; $D apps | sed "s/^/    /"
OUT=$($D deploys nodeb 2>&1); contains "deploys" succeeded "$OUT"
OUT=$($D env nodeb set APIKEY=abc123 --secret --restart 2>&1); contains "env set + restart" "reiniciado" "$OUT"
contains "env list mascara" "APIKEY=••••" "$($D env nodeb list)"
check "env reveal" abc123 "$($D env nodeb reveal APIKEY)"
OUT=$($D logs py -n 5 2>&1); [ -n "$OUT" ] && ok "logs de runtime do python" || bad "logs vazios"
wait_until 15 '[ "$(atcode nodeb.test.dev /)" = 200 ]'
printf '// v2\n' >> "$W/nodeb/server.js"; publish "$W/nodeb" "v2"
OUT=$($D deploy nodeb --rebuild 2>&1); RC=$?; check "deploy --rebuild" 0 "$RC"
OUT=$($D rollback nodeb 2>&1); RC=$?; check "droid rollback" 0 "$RC"; contains "rollback concluído" succeeded "$OUT"
wait_until 10 '[ "$($D apps --json | python3 -c "import sys,json; print([a[\"state\"] for a in json.load(sys.stdin) if a[\"name\"]==\"nodeb\"][0])")" = running ]'
check "estado após rollback" running "$($D apps --json | python3 -c 'import sys,json; print([a["state"] for a in json.load(sys.stdin) if a["name"]=="nodeb"][0])')"
$D stop py >/dev/null; wait_until 10 '[ "$(sv status "$E2E_HOME/sv/dpt-app-py" | cut -d: -f1)" = down ]'
check "droid stop" down "$(sv status "$E2E_HOME/sv/dpt-app-py" | cut -d: -f1)"
wait_until 10 '[ "$(atcode py.test.dev /)" = 503 ]'; check "app parado → 503 no proxy" 503 "$(atcode py.test.dev /)"
OUT=$($D start py 2>&1); RC=$?; check "droid start" 0 "$RC"
wait_until 15 '[ "$(atcode py.test.dev /)" = 200 ]'; check "py de volta" 200 "$(atcode py.test.dev /)"
$D apps rm cust --yes >/dev/null; check "app removido" 404 "$(code GET /apps/cust)"
test ! -e "$E2E_HOME/sv/dpt-app-cust" && ok "serviço removido" || bad "serviço do app removido ainda existe"

e2e_teardown
