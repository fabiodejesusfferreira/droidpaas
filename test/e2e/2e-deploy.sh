#!/usr/bin/env bash
# 2E — build, deploy, rollback automático/manual, env e site estático com runit + Caddy reais.
# (AT-03 sem o git push, AT-04, AT-05, AT-06, AT-07, AT-08, AT-12)
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup

W=$E2E_HOME/work; rm -rf "$W"; mkdir -p "$W/web" "$W/site"
gitc() { git -C "$1" -c user.name=e2e -c user.email=e2e@e2e "${@:2}"; }

newapp() { # newapp nome dir
  api POST /apps "{\"name\":\"$1\"}" >/dev/null
  git init -q --bare -b main "$E2E_HOME/home/apps/$1/repo.git"
  gitc "$2" init -q -b main; gitc "$2" remote add origin "$E2E_HOME/home/apps/$1/repo.git"
}
publish() { gitc "$1" add -A; gitc "$1" commit -qm "$2" --allow-empty; gitc "$1" push -q origin main; }
deploy() { # deploy app [json] -> imprime "status"
  local id; id=$(api POST "/apps/$1/deploys" "${2:-{\}}" | jget id)
  for _ in $(seq 1 240); do
    s=$(api GET "/deploys/$id" | jget status)
    case "$s" in queued|running) sleep 0.5;; *) echo "$id $s"; return;; esac
  done
  echo "$id timeout"
}
nodeapp() { # nodeapp versão [extra]
  cat > "$W/web/server.js" <<JS
const http = require("http");
http.createServer((q, r) => {
  if (q.url === "/health") return r.end("ok");
  if (q.url.startsWith("/env/")) return r.end(String(process.env[q.url.slice(5)]));
  r.end("$1\n");
}).listen(process.env.PORT, process.env.HOST);
JS
  echo '{"name":"web","version":"1.0.0","scripts":{"start":"node server.js"}}' > "$W/web/package.json"
}

say "app web (node)"
newapp web "$W/web"
api POST /apps/web/domains '{"hostname":"web.test.dev"}' >/dev/null
api PATCH /apps/web '{"health_path":"/health"}' >/dev/null

say "v1"
nodeapp v1; publish "$W/web" "v1"
t0=$(date +%s)
read -r ID ST <<<"$(deploy web)"
check "deploy v1" succeeded "$ST"
echo "  deploy v1: $(( $(date +%s) - t0 )) s (RNF-05: ≤ 90 s)"
wait_until 15 test "$(at web.test.dev /)" = v1
check "v1 servida pelo Caddy" v1 "$(at web.test.dev / | tr -d '\n')"
check "estado do app" running "$(api GET /apps/web | jget state)"
V1=$(readlink "$E2E_HOME/home/apps/web/current")

say "v2"
nodeapp v2; publish "$W/web" "v2"
read -r ID ST <<<"$(deploy web)"
check "deploy v2" succeeded "$ST"
wait_until 15 test "$(at web.test.dev /)" = v2
check "v2 servida" v2 "$(at web.test.dev / | tr -d '\n')"

say "AT-04: build com erro não derruba a v2"
echo 'build = "echo quebrou-de-proposito; exit 3"' > "$W/web/droidpaas.toml"; publish "$W/web" "build quebrado"
read -r ID ST <<<"$(deploy web)"
check "deploy com build quebrado" failed "$ST"
contains "log tem a saída do build" quebrou-de-proposito "$(api GET /deploys/$ID/logs)"
check "v2 continua no ar" v2 "$(at web.test.dev / | tr -d '\n')"
rm "$W/web/droidpaas.toml"

say "AT-05: release que sobe mas falha no health → rollback automático"
echo 'process.exit(1)' > "$W/web/server.js"; publish "$W/web" "app quebrado"
api PATCH /apps/web '{"health_timeout_s":6}' >/dev/null
read -r ID ST <<<"$(deploy web)"
check "deploy com app quebrado" rolled_back "$ST"
sleep 2
check "v2 de volta no ar" v2 "$(at web.test.dev / | tr -d '\n')"

say "AT-06: rollback manual para a v1"
nodeapp v2; publish "$W/web" "v2 de novo"   # histórico limpo para o HEAD
RID=$(api GET /apps/web/releases | python3 -c 'import sys,json; d=json.load(sys.stdin); print([r["id"] for r in d["releases"] if r["status"]=="ready"][-1])')
read -r ID ST <<<"$(api POST /apps/web/rollback "{\"release_id\":$RID}" | jget id) x"
for _ in $(seq 1 60); do s=$(api GET /deploys/$ID | jget status); [ "$s" = running ] || [ "$s" = queued ] || break; sleep 0.5; done
check "rollback manual" succeeded "$s"
wait_until 15 test "$(at web.test.dev /)" = v1
check "v1 de volta" v1 "$(at web.test.dev / | tr -d '\n')"

say "AT-07: variável de ambiente + reiniciar"
OUT=$(api PUT "/apps/web/env?restart=true" '{"set":{"FOO":{"value":"bar"},"SEGREDO":{"value":"s3cr3t","secret":true}}}')
contains "API mascara o segredo" "••••" "$OUT"
case "$OUT" in *s3cr3t*) bad "segredo vazou na resposta";; *) ok "segredo não vazou";; esac
wait_until 20 test "$(at web.test.dev /env/FOO)" = bar
check "FOO visível ao app" bar "$(at web.test.dev /env/FOO)"
check "SEGREDO visível ao app" s3cr3t "$(at web.test.dev /env/SEGREDO)"
check "NODE_ENV injetado" production "$(at web.test.dev /env/NODE_ENV)"

say "AT-08: kill -9 no app → reinício em ≤ 5 s"
PID=$(svpid dpt-app-web); kill -9 "$PID"; t0=$(date +%s)
wait_until 8 test "$(svpid dpt-app-web)" != "$PID"
echo "  novo pid em $(( $(date +%s) - t0 )) s"
wait_until 8 test "$(at web.test.dev /env/FOO)" = bar
check "app voltou" bar "$(at web.test.dev /env/FOO)"

say "AT-12: site estático servido pelo Caddy, sem processo"
newapp site "$W/site"
api POST /apps/site/domains '{"hostname":"site.test.dev"}' >/dev/null
echo '<h1>site estatico</h1>' > "$W/site/index.html"; publish "$W/site" "site"
read -r ID ST <<<"$(deploy site)"
check "deploy estático" succeeded "$ST"
wait_until 15 test "$(atcode site.test.dev /)" = 200
contains "conteúdo estático" "site estatico" "$(at site.test.dev /)"
check "sem processo para o site" down "$(sv status "$E2E_HOME/sv/dpt-app-site" | cut -d: -f1)"

e2e_teardown
