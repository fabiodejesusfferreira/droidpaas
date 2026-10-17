#!/usr/bin/env bash
# Fase 3 — painel web no aparelho: login/sessão/CSRF, dispositivo real (Termux:API), desempenho (RNF-04),
# deploy pelo painel com log ao vivo, logs de runtime por SSE, cabeçalhos e cookie Secure atrás do Caddy.
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup

W=$E2E_HOME/work; rm -rf "$W"; mkdir -p "$W/web"
JAR=$E2E_HOME/jar.txt; rm -f "$JAR"
PW=senha-de-teste-12345
P=http://$PANEL
pc() { curl -s -m 20 -b "$JAR" -c "$JAR" "$@"; }
login_panel() { # login_panel [base-url] [header...]
  local base=${1:-$P}; shift
  local tok; tok=$(curl -s -c "$JAR" "$@" "$base/login" | grep -o 'name="_csrf" value="[^"]*"' | head -1 | sed 's/.*value="//;s/"$//')
  curl -s -i -b "$JAR" -c "$JAR" "$@" --data-urlencode "_csrf=$tok" --data-urlencode username=admin --data-urlencode "password=$PW" --data-urlencode next=/ "$base/login"
}
csrf() { pc "${1:-$P}/" | grep -o 'X-CSRF-Token":"[^"]*' | sed 's/.*:"//'; }
ms() { awk -v s="$1" 'BEGIN{printf "%d", s*1000}'; }

say "sem sessão: redireciona para /login"
check "GET / → 303" 303 "$(curl -s -o /dev/null -w '%{http_code}' $P/)"
check "Location" "/login?next=%2F" "$(curl -s -o /dev/null -w '%{redirect_url}' $P/ | sed "s#$P##")"
check "assets públicos" 200 "$(curl -s -o /dev/null -w '%{http_code}' $P/static/app.css)"
H=$(curl -s -D- -o /dev/null $P/login)
contains "CSP no login" "Content-Security-Policy: default-src 'self'" "$H"
contains "frame-ancestors none" "frame-ancestors 'none'" "$H"
contains "nosniff" "X-Content-Type-Options: nosniff" "$H"

say "login (AT-20)"
R=$(login_panel)
contains "login redireciona" "HTTP/1.1 303" "$R"
contains "cookie HttpOnly+SameSite=Lax" "HttpOnly; SameSite=Lax" "$(printf '%s' "$R" | grep -i '^set-cookie: dp_session')"
R=$(curl -s -i -c /dev/null --data-urlencode username=admin --data-urlencode password=errada $P/login | head -1); contains "login sem token CSRF é recusado" " 403" "$R"
for i in 1 2 3 4 5 6; do
  J=$E2E_HOME/j3.txt; rm -f $J
  tk=$(curl -s -c $J $P/login | grep -o 'name="_csrf" value="[^"]*"' | head -1 | sed 's/.*value="//;s/"$//')
  c=$(curl -s -o /dev/null -w '%{http_code}' -b $J -c $J --data-urlencode "_csrf=$tk" --data-urlencode username=intruso --data-urlencode password=errada $P/login); LAST=$c
done
check "6ª tentativa errada do mesmo usuário → 429" 429 "$LAST"

say "dashboard: dispositivo real e desempenho (RNF-04)"
api POST /apps '{"name":"blog"}' >/dev/null
api POST /apps '{"name":"loja"}' >/dev/null
D=$(pc $P/)
contains "página do painel" "Dispositivo" "$D"
contains "bateria do Termux:API" "°C" "$D"
contains "RAM" "MB livre" "$D"
contains "disco" "GB livre" "$D"
contains "apps listados" 'href="/apps/blog"' "$D"
T=(); for i in $(seq 1 30); do T+=($(ms "$(pc -o /dev/null -w '%{time_total}' $P/)")); done
SORTED=($(printf '%s\n' "${T[@]}" | sort -n)); P95=${SORTED[28]}; MED=${SORTED[15]}
echo "  dashboard: mediana ${MED} ms · p95 ${P95} ms · máx ${SORTED[29]} ms (RNF-04: p95 ≤ 300 ms)"
[ "$P95" -le 300 ] && ok "p95 ≤ 300 ms" || bad "p95 = ${P95} ms"
T=(); for i in $(seq 1 15); do T+=($(ms "$(pc -o /dev/null -w '%{time_total}' $P/apps/blog)")); done; SORTED=($(printf '%s\n' "${T[@]}" | sort -n)); echo "  página do app: p95 ${SORTED[13]} ms"

say "CSRF (AT-21)"
TOK=$(csrf)
check "POST sem token → 403" 403 "$(pc -o /dev/null -w '%{http_code}' -X POST $P/apps/blog/action/restart)"
check "token errado → 403" 403 "$(pc -o /dev/null -w '%{http_code}' -X POST --data-urlencode _csrf=zzz $P/apps/blog/action/restart)"
check "Origin externo → 403" 403 "$(pc -o /dev/null -w '%{http_code}' -X POST -H 'Origin: https://evil.example' --data-urlencode "_csrf=$TOK" $P/apps/blog/action/restart)"
check "token certo → 303" 303 "$(pc -o /dev/null -w '%{http_code}' -X POST --data-urlencode "_csrf=$TOK" $P/apps/blog/action/restart)"

say "deploy pelo git → painel mostra o log (AT-22/AT-23)"
newapp pnl "$W/web"
api POST /apps/pnl/domains '{"hostname":"pnl.test.dev"}' >/dev/null
api PATCH /apps/pnl '{"health_path":"/health"}' >/dev/null
nodeapp v1
TOK=$(csrf)
LOC=$(pc -o /dev/null -w '%{redirect_url}' -X POST --data-urlencode "_csrf=$TOK" $P/apps/pnl/deployments)
contains "botão Deploy sem commits: erro amigável" "/apps/pnl/deployments" "$LOC"
contains "mensagem de ref inexistente" "não existe no repositório" "$(pc $P/apps/pnl/deployments)"
publish "$W/web" v1
LOC=$(pc -o /dev/null -w '%{redirect_url}' -X POST --data-urlencode "_csrf=$TOK" $P/apps/pnl/deployments)
contains "redireciona para o deploy" "/apps/pnl/deployments/1" "$LOC"
LIVE=$(pc "$LOC"); contains "página do deploy tem o visualizador de log" "data-log" "$LIVE"
for _ in $(seq 1 60); do s=$(api GET /deploys/1 | jget status); [ "$s" = running ] || [ "$s" = queued ] || break; sleep 1; done
check "deploy concluído" succeeded "$s"
PG=$(pc $P/apps/pnl/deployments/1)
contains "log do deploy no HTML" "-----&gt; finalize" "$PG"
contains "badge concluído" "concluído" "$PG"
wait_until 20 '[ "$(at pnl.test.dev /)" = v1 ]'
check "app servido" v1 "$(at pnl.test.dev / | tr -d '\n')"
wait_until 15 'pc $P/apps/pnl | grep -q "no ar"'
APPPG=$(pc $P/apps/pnl); contains "painel mostra app no ar" "no ar" "$APPPG" || printf '%s\n' "$APPPG" | grep -i "badge\|state" | head -5
wait_until 40 'pc $P/ | grep -q "KB\|MB · "'
contains "recursos (RSS) do app no dashboard" " · " "$(pc $P/fragments/apps)"

say "logs de runtime ao vivo por SSE (AT-25)"
( pc -N -m 7 "$P/apps/pnl/logs/stream" > "$E2E_HOME/sse.out" 2>&1 & )
sleep 2; at pnl.test.dev /ao-vivo-do-painel >/dev/null; sleep 4
contains "linha nova chega pelo stream" "GET /ao-vivo-do-painel" "$(cat "$E2E_HOME/sse.out")"
contains "evento line com ts/text" 'event: line' "$(cat "$E2E_HOME/sse.out")"
contains "download dos logs" "ao-vivo-do-painel" "$(pc "$P/apps/pnl/logs/download")"

say "variáveis de ambiente pelo painel (AT-24)"
TOK=$(csrf)
pc -o /dev/null -X POST --data-urlencode "_csrf=$TOK" --data-urlencode k=SEGREDO --data-urlencode v=valor-super-secreto --data-urlencode saa=1 --data-urlencode restart=1 $P/apps/pnl/environment
ENV=$(pc $P/apps/pnl/environment); contains "segredo mascarado no HTML" "••••" "$ENV"
case "$ENV" in *valor-super-secreto*) bad "segredo vazou no HTML";; *) ok "segredo não vazou";; esac
REV=$(pc -X POST -H 'HX-Request: true' --data-urlencode "_csrf=$TOK" --data-urlencode key=SEGREDO $P/apps/pnl/environment/reveal); contains "revelar (htmx)" "valor-super-secreto" "$REV"
wait_until 20 '[ "$(at pnl.test.dev /env/SEGREDO)" = valor-super-secreto ]'
check "app enxerga o valor após Salvar e reiniciar" valor-super-secreto "$(at pnl.test.dev /env/SEGREDO)"

say "painel por hostname atrás do Caddy: cookie Secure com X-Forwarded-Proto (túnel)"
api PUT /settings '{"panel_hostname":"painel.test.dev"}' >/dev/null
wait_until 20 '[ "$(atcode painel.test.dev /login)" = 200 ]'
check "painel via Caddy (Host)" 200 "$(atcode painel.test.dev /login)"
# o libcurl recente ignora cookies Secure recebidos por http: lê os Set-Cookie dos cabeçalhos e reenvia à mão
XF=(-H "Host: painel.test.dev" -H "X-Forwarded-Proto: https")
cookies() { awk 'tolower($1)=="set-cookie:" {sub(/;.*/,"",$2); printf "%s%s", sep, $2; sep="; "}' ; }
H1=$(curl -s -i "${XF[@]}" "http://$PROXY/login"); tok=$(printf '%s' "$H1" | grep -o 'name="_csrf" value="[^"]*"' | head -1 | sed 's/.*value="//;s/"$//')
contains "cookie de login é Secure atrás do proxy confiável" "Secure" "$(printf '%s' "$H1" | grep -i '^set-cookie: dp_login_csrf')"
R=$(curl -s -i -H "Cookie: $(printf '%s' "$H1" | tr -d '\r' | cookies)" "${XF[@]}" -H "Origin: https://painel.test.dev" --data-urlencode "_csrf=$tok" --data-urlencode username=admin --data-urlencode "password=$PW" "http://$PROXY/login")
contains "login via Caddy" "HTTP/1.1 303" "$R"
contains "cookie Secure atrás do proxy confiável" "Secure" "$(printf '%s' "$R" | grep -i '^set-cookie: dp_session')"

say "logout invalida a sessão"
rm -f "$JAR"; login_panel >/dev/null; TOK=$(csrf); cp "$JAR" "$E2E_HOME/jar-old.txt"
pc -o /dev/null -X POST --data-urlencode "_csrf=$TOK" $P/logout
check "depois do logout" 303 "$(pc -o /dev/null -w '%{http_code}' $P/)"
check "cookie antigo não volta a valer" 303 "$(curl -s -o /dev/null -w '%{http_code}' -b "$E2E_HOME/jar-old.txt" $P/)"

e2e_teardown
