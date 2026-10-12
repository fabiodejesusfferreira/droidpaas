#!/usr/bin/env bash
# 2D — proxy Caddy real: 404, 503, restart do Caddy e queda do droidd (RNF-08).
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup

say "dp-caddy criado e config aplicada"
wait_until 30 curl -sf "http://$ADMIN/config/" || bad "Admin API do Caddy não respondeu"
check "host desconhecido → 404" 404 "$(atcode nada.local /)"
contains "página 404 do DroidPaaS" "nenhum app para este host" "$(at nada.local /)"

say "app sem release → 503"
api POST /apps '{"name":"blog"}' >/dev/null
api POST /apps/blog/domains '{"hostname":"blog.local.dev"}' >/dev/null
wait_until 15 test "$(atcode blog.local.dev /)" = 503
check "app sem release → 503" 503 "$(atcode blog.local.dev /)"
contains "página 503 do DroidPaaS" "parado ou ainda não tem" "$(at blog.local.dev /)"

say "Caddy reinicia sozinho (kill -9) e volta com as rotas"
PID=$(svpid dpt-caddy)
kill -9 "$PID"
wait_until 15 test "$(atcode blog.local.dev /)" = 503
check "rota 503 de volta após o restart" 503 "$(atcode blog.local.dev /)"
NEW=$(svpid dpt-caddy)
[ "$NEW" != "$PID" ] && ok "pid mudou ($PID → $NEW)" || bad "pid não mudou"

say "droidd cai: o proxy segue servindo (RNF-08)"
kill -9 "$(cat "$E2E_HOME/droidd.pid")"
sleep 1
check "proxy com droidd morto" 503 "$(atcode blog.local.dev /)"
check "404 com droidd morto" 404 "$(atcode nada.local /)"
e2e_teardown
