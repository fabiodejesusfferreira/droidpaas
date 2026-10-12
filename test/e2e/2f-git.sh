#!/usr/bin/env bash
# 2F — git push por SSH de verdade (sshd próprio do sandbox, command= forçado, post-receive → droidd):
# AT-03, AT-04 via push, restrições do git-shell e remoção de chave.
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup
e2e_sshd

W=$E2E_HOME/work; rm -rf "$W"; mkdir -p "$W/web"
gitc() { git -C "$W/web" -c user.name=e2e -c user.email=e2e@e2e "$@"; }
nodeapp() {
  cat > "$W/web/server.js" <<JS
const http = require("http");
http.createServer((q, r) => r.end(q.url === "/health" ? "ok" : "$1\n")).listen(process.env.PORT, process.env.HOST);
JS
  echo '{"name":"web","version":"1.0.0","scripts":{"start":"node server.js"}}' > "$W/web/package.json"
}

say "cadastra a chave SSH pela API"
PUB=$(cat "$E2E_HOME/ssh/client.pub")
R=$(api POST /ssh-keys "{\"name\":\"e2e\",\"public_key\":\"$PUB\"}")
contains "chave cadastrada" SHA256: "$R"
contains "bloco gerenciado no authorized_keys" "BEGIN DROIDPAAS" "$(cat "$E2E_HOME/authorized_keys")"
contains "comando forçado e sem pty" 'git-shell --home' "$(cat "$E2E_HOME/authorized_keys")"
contains "no-pty" no-pty "$(cat "$E2E_HOME/authorized_keys")"

say "app + remote git"
R=$(api POST /apps '{"name":"web"}')
SSHURL=$(printf '%s' "$R" | jget git.ssh)
contains "URL do remote SSH" ":$SSH_PORT/web.git" "$SSHURL"
api POST /apps/web/domains '{"hostname":"web.test.dev"}' >/dev/null
api PATCH /apps/web '{"health_path":"/health"}' >/dev/null
test -x "$E2E_HOME/home/apps/web/repo.git/hooks/post-receive" && ok "hook post-receive instalado" || bad "hook ausente"
gitc init -q -b main
REMOTE="ssh://$SSH_USER@127.0.0.1:$SSH_PORT/web.git"
gitc remote add droid "$REMOTE"

say "AT-03: git push → build visível no terminal → app responde pelo hostname"
nodeapp v1; gitc add -A; gitc commit -qm v1
OUT=$(gitc push droid main 2>&1); RC=$?
check "git push saiu com 0" 0 "$RC"
contains "build visível como remote:" "remote: DroidPaaS: deploy #1" "$OUT"
contains "estágio install/prepare no terminal" "-----> prepare" "$OUT"
contains "deploy finalizado" "succeeded" "$OUT"
wait_until 15 test "$(at web.test.dev /)" = v1
check "v1 pelo hostname" v1 "$(at web.test.dev / | tr -d '\n')"

say "AT-04 via push: build quebrado, v1 segue no ar"
nodeapp v2; echo 'build = "echo erro-no-build; exit 5"' > "$W/web/droidpaas.toml"
gitc add -A; gitc commit -qm "v2 quebrada"
OUT=$(gitc push droid main 2>&1); RC=$?
check "o push em si é aceito (commit recebido)" 0 "$RC"
contains "erro do build aparece no terminal" erro-no-build "$OUT"
contains "aviso de falha" "release anterior segue no ar" "$OUT"
check "v1 continua no ar" v1 "$(at web.test.dev / | tr -d '\n')"
check "deploy #2 failed" failed "$(api GET /deploys/2 | jget status)"
rm "$W/web/droidpaas.toml"; gitc add -A; gitc commit -qm "v2 boa"
OUT=$(gitc push droid main 2>&1)
contains "v2 boa: sucesso" succeeded "$OUT"
wait_until 15 test "$(at web.test.dev /)" = v2
check "v2 no ar" v2 "$(at web.test.dev / | tr -d '\n')"

say "push em outro branch não dispara deploy"
gitc checkout -q -b feature; OUT=$(gitc push droid feature 2>&1)
contains "mensagem de branch ignorado" "só o branch" "$OUT"
check "nenhum deploy novo" 3 "$(api GET /apps/web/deploys | python3 -c 'import sys,json; print(len(json.load(sys.stdin)))')"
gitc checkout -q main

say "restrições do command= forçado"
OUT=$($GIT_SSH_COMMAND "$SSH_USER@127.0.0.1" ls 2>&1); contains "ls negado" "acesso negado" "$OUT"
OUT=$($GIT_SSH_COMMAND "$SSH_USER@127.0.0.1" 2>&1 </dev/null); contains "shell interativo negado" "acesso negado" "$OUT"
OUT=$(git ls-remote "ssh://$SSH_USER@127.0.0.1:$SSH_PORT/nada.git" 2>&1); contains "app inexistente" "app inexistente" "$OUT"
OUT=$(git ls-remote "ssh://$SSH_USER@127.0.0.1:$SSH_PORT/../../etc.git" 2>&1); contains "traversal negado" "acesso negado" "$OUT"
ssh-keygen -q -t ed25519 -N '' -f "$E2E_HOME/ssh/intruso"
OUT=$(ssh -i "$E2E_HOME/ssh/intruso" -p $SSH_PORT -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes "$SSH_USER@127.0.0.1" ls 2>&1)
contains "chave não cadastrada não entra" "Permission denied" "$OUT"

say "remover a chave pela API fecha o acesso"
KID=$(api GET /ssh-keys | jget 0.id)
check "DELETE chave" 204 "$(code DELETE /ssh-keys/$KID)"
case "$(cat "$E2E_HOME/authorized_keys")" in *git-shell*) bad "chave ainda no authorized_keys";; *) ok "authorized_keys limpo";; esac
OUT=$(gitc push droid main 2>&1); contains "push sem chave é recusado" "Permission denied" "$OUT"

e2e_teardown
