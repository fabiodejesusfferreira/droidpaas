#!/usr/bin/env bash
# 2G — reconciliador: queda do droidd (AT-10), crash loop (AT-09), logs de runtime, serviços órfãos.
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup

W=$E2E_HOME/work; rm -rf "$W"; mkdir -p "$W/web"
newapp web "$W/web"
api POST /apps/web/domains '{"hostname":"web.test.dev"}' >/dev/null
api PATCH /apps/web '{"health_path":"/health"}' >/dev/null
nodeapp v1; publish "$W/web" v1
read -r ID ST <<<"$(deploy web)"
check "deploy v1" succeeded "$ST"
wait_until 15 '[ "$(at web.test.dev /)" = v1 ]'

say "AT-10: o droidd morre (kill -9) e apps + proxy seguem no ar"
PID_APP=$(svpid dpt-app-web); PID_CADDY=$(svpid dpt-caddy)
kill -9 "$(cat "$E2E_HOME/droidd.pid")"; sleep 3
check "app segue respondendo" v1 "$(at web.test.dev / | tr -d '\n')"
check "mesmo pid do app" "$PID_APP" "$(svpid dpt-app-web)"
check "mesmo pid do Caddy" "$PID_CADDY" "$(svpid dpt-caddy)"
start_droidd && ok "droidd voltou" || bad "droidd não voltou"
check "API volta com o estado" running "$(api GET /apps/web | jget state)"
check "app não foi reiniciado pelo reconciliador" "$PID_APP" "$(svpid dpt-app-web)"
check "deploys sobreviveram" succeeded "$(api GET /deploys/$ID | jget status)"

say "logs de runtime (tail e follow por SSE)"
at web.test.dev /um >/dev/null; at web.test.dev /dois >/dev/null
OUT=$(curl -s -H "Authorization: Bearer $TOKEN" "$API/apps/web/logs?tail=20")
contains "tail traz o stdout do app" "listening on" "$OUT"
contains "tail traz as requisições" "GET /dois" "$OUT"
(curl -sN -m 6 -H "Authorization: Bearer $TOKEN" "$API/apps/web/logs?follow=1&tail=0" > "$E2E_HOME/follow.out" 2>&1 &)
sleep 1.5; at web.test.dev /ao-vivo >/dev/null; sleep 2.5
contains "follow entrega a linha nova (≤ ~2 s)" "GET /ao-vivo" "$(cat "$E2E_HOME/follow.out")"
contains "evento SSE line com ts" '"ts":"20' "$(cat "$E2E_HOME/follow.out")"

say "AT-09: crash loop → crashed, sem reinício infinito, evento com logs"
for i in $(seq 1 10); do
  P=$(svpid dpt-app-web); [ -n "$P" ] && kill -9 "$P" 2>/dev/null
  sleep 2.2
  [ "$(api GET /apps/web | jget state)" = crashed ] && break
done
check "estado crashed" crashed "$(api GET /apps/web | jget state)"
check "serviço parado" down "$(sv status "$E2E_HOME/sv/dpt-app-web" | cut -d: -f1)"
wait_until 10 '[ "$(atcode web.test.dev /)" = 503 ]'
check "proxy devolve 503 para app crashed" 503 "$(atcode web.test.dev /)"
EV=$(api GET "/events?limit=50" | python3 -c 'import sys,json; print(json.dumps([e for e in json.load(sys.stdin) if e["kind"]=="app.crash_loop"]))')
contains "evento app.crash_loop" app.crash_loop "$EV"
contains "evento traz as últimas linhas do log" "listening on" "$EV"
echo "  evento: $(printf %s "$EV" | head -c 600)"; ls -la "$E2E_HOME/home/logs/apps/web/" | tail -3
sleep 6
check "continua parado (sem reinício infinito)" down "$(sv status "$E2E_HOME/sv/dpt-app-web" | cut -d: -f1)"
api POST /apps/web/start >/dev/null
wait_until 15 '[ "$(at web.test.dev /)" = v1 ]'
check "Start tira de crashed" v1 "$(at web.test.dev / | tr -d '\n')"
wait_until 10 '[ "$(api GET /apps/web | jget state)" = running ]'
check "estado running" running "$(api GET /apps/web | jget state)"

say "serviço órfão é removido depois do período de graça"
mkdir -p "$E2E_HOME/sv/dpt-app-fantasma"
printf '#!/data/data/com.termux/files/usr/bin/sh\nexec sleep 1000\n' > "$E2E_HOME/sv/dpt-app-fantasma/run"; chmod +x "$E2E_HOME/sv/dpt-app-fantasma/run"
wait_until 10 test -e "$E2E_HOME/sv/dpt-app-fantasma/supervise/ok" && ok "runsvdir adotou o fantasma"
wait_until 25 test ! -e "$E2E_HOME/sv/dpt-app-fantasma" && ok "órfão removido" || bad "órfão continua lá"
check "serviço do app real intacto" run "$(sv status "$E2E_HOME/sv/dpt-app-web" | cut -d: -f1)"

e2e_teardown
