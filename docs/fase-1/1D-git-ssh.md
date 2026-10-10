# Bloco 1D — Git por SSH, `git http-backend` e socket Unix

Cobre as atividades 1.6 e 1.7 (parte) e as hipóteses **V-06** (`sshd` respeita `command=` e o `git-receive-pack` funciona), **V-07** (`git http-backend` disponível) e **V-12** (socket Unix em `$HOME`, inclusive chamado de dentro do hook do git).

Reproduz à mão o `droid git-shell` e o `post-receive` da Fase 2 (TSD §8.4). O roteiro é **local primeiro** (tudo no próprio celular, via `localhost`, sem depender de rede); o push do PC é a Parte 5, opcional.

> Cole **um bloco por vez** e espere o prompt voltar. Se aparecer `>` no início da linha, o shell está esperando fechar um bloco: `Ctrl+C` e recomece.
> Não defina senha no Termux (`passwd`): só chave pública. O `sshd` escuta em `0.0.0.0:8022`.

## 1. Preparação

```bash
mkdir -p ~/poc/git ~/poc/run ~/poc/work
git init -q --bare ~/poc/git/poc-app.git
ls ~/.ssh/ssh_host_* >/dev/null 2>&1 || ssh-keygen -A
ssh-keygen -t ed25519 -N '' -f ~/.ssh/poc_git -q && echo "chave criada"
whoami
```

## 2. O "git-shell" forçado (V-06)

Aceita só `git-receive-pack`/`git-upload-pack` de um app válido (regex do nome, sem `/` nem `..`), como o `droid git-shell` fará.

```bash
cat > ~/poc/git-shell.sh <<'EOF'
#!/data/data/com.termux/files/usr/bin/sh
PATH=/data/data/com.termux/files/usr/bin:$PATH
deny() { echo "DroidPaaS: acesso negado" >&2; exit 1; }
cmd="${SSH_ORIGINAL_COMMAND:-}"
case "$cmd" in
  "git-receive-pack '"*"'"|"git-upload-pack '"*"'") ;;
  *) deny ;;
esac
op=${cmd%% *}
arg=${cmd#*\'}; arg=${arg%\'}; arg=${arg#/}
app=${arg%.git}
[ "$app.git" = "$arg" ] || deny
case "$app" in ""|[!a-z]*|*[!a-z0-9-]*) deny ;; esac
repo=$HOME/poc/git/$app.git
[ -d "$repo" ] || { echo "DroidPaaS: app inexistente" >&2; exit 1; }
exec git "${op#git-}" "$repo"
EOF
chmod +x ~/poc/git-shell.sh
```

Instale a chave com o comando forçado (a chave `poc_git` só serve para git, nunca para shell):

```bash
cp -n ~/.ssh/authorized_keys ~/.ssh/authorized_keys.bak 2>/dev/null
printf 'command="%s",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s\n' \
  "$HOME/poc/git-shell.sh" "$(cat ~/.ssh/poc_git.pub)" >> ~/.ssh/authorized_keys
chmod 700 ~/.ssh; chmod 600 ~/.ssh/authorized_keys
pgrep -a sshd || sshd
sleep 2; pgrep -a sshd
```

Se `sshd` não subir, copie a mensagem de erro. Pergunta registrada no relatório: porta 8022 confirmada?

## 3. Testes de restrição e de leitura

Defina a variável da sessão:

```bash
export GIT_SSH_COMMAND="ssh -i $HOME/.ssh/poc_git -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -p 8022"
```

```bash
ssh -i ~/.ssh/poc_git -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -p 8022 localhost 'ls'
echo "exit=$?"
```
Esperado: `DroidPaaS: acesso negado` e `exit=1` (qualquer comando que não seja git é bloqueado).

```bash
ssh -i ~/.ssh/poc_git -o IdentitiesOnly=yes -p 8022 localhost
echo "exit=$?"
```
Esperado: `acesso negado` também (sem comando, sem PTY).

```bash
git ls-remote ssh://localhost/poc-app.git; echo "exit=$?"
git ls-remote ssh://localhost/outro.git; echo "exit=$?"
git ls-remote "ssh://localhost/../../etc.git"; echo "exit=$?"
```
Esperado: o 1º `exit=0` (repo vazio, sem saída); o 2º `app inexistente` e `exit=128`; o 3º `acesso negado`.

## 4. Push com hook e socket Unix (V-06 + V-12)

Servidor de socket Unix de teste (parte do V-12):

```bash
cat > ~/poc/sock.js <<'EOF'
const http=require("http"),fs=require("fs"),p=process.env.HOME+"/poc/run/t.sock";
try{fs.unlinkSync(p)}catch{}
http.createServer((q,r)=>{let b="";q.on("data",d=>b+=d);q.on("end",()=>{console.log("hook:",b.trim());r.end("socket ok: "+b)})}).listen(p,()=>fs.chmodSync(p,0o600));
EOF
node ~/poc/sock.js > ~/poc/sock.log 2>&1 &
sleep 1; ls -l ~/poc/run/t.sock
curl -s --unix-socket ~/poc/run/t.sock http://x/ping; echo
```
Esperado: o arquivo `srw-------` e `socket ok:` na resposta.

Hook `post-receive` de teste: lê as linhas do stdin, simula build com saída gradual e chama o socket:

```bash
cat > ~/poc/git/poc-app.git/hooks/post-receive <<'EOF'
#!/bin/sh
echo "== DroidPaaS post-receive =="
for i in 1 2 3; do echo "build passo $i..."; sleep 1; done
cat | curl -s --unix-socket "$HOME/poc/run/t.sock" --data-binary @- http://x/hook
echo
echo "== fim =="
EOF
chmod +x ~/poc/git/poc-app.git/hooks/post-receive
```

Commit de teste e push:

```bash
cd ~/poc/work && git init -q -b main 2>/dev/null
echo "primeiro" > README
git add README
git -c user.name=poc -c user.email=poc@local commit -qm "primeiro"
git remote add droid ssh://localhost/poc-app.git 2>/dev/null
git push droid main
echo "exit=$?"
cat ~/poc/sock.log
```
Esperado: no terminal do `git push`, linhas `remote: == DroidPaaS post-receive ==`, `remote: build passo 1...` **aparecendo em tempo real** (uma por segundo, não todas no fim), `remote: socket ok: ...` e `exit=0`; o `sock.log` contém a linha `hook: 0000… <sha> refs/heads/main`.

Se as linhas `remote:` só aparecerem todas de uma vez no final, anote: afeta a experiência de acompanhar o build pelo `git push` (RF-04).

## 5. `git http-backend` (V-07)

```bash
ls $(git --exec-path)/git-http-backend
```
```bash
GIT_PROJECT_ROOT=$HOME/poc/git GIT_HTTP_EXPORT_ALL=1 REQUEST_METHOD=GET \
PATH_INFO=/poc-app.git/info/refs QUERY_STRING='service=git-upload-pack' \
$(git --exec-path)/git-http-backend | head -5
```
Esperado: cabeçalho `Content-Type: application/x-git-upload-pack-advertisement` e a linha com `refs/heads/main`. Se o binário não existir, V-07 falha (o RF-05 vira "descartado" ou implementa-se Git HTTP de outro jeito).

## 6. Opcional: push do PC (fidelidade ao uso real)

No **PC**, com a mesma rede Wi-Fi do celular:

```bash
ssh-keygen -t ed25519 -N '' -f ~/.ssh/droidpaas_poc
mkdir -p /tmp/pubshare && cp ~/.ssh/droidpaas_poc.pub /tmp/pubshare/
python3 -m http.server 8099 --bind <IP_DO_PC> --directory /tmp/pubshare
```

No **celular** (só a chave pública trafega; pare o servidor do PC depois com `Ctrl+C`):

```bash
printf 'command="%s",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s\n' \
  "$HOME/poc/git-shell.sh" "$(curl -s http://<IP_DO_PC>:8099/droidpaas_poc.pub)" >> ~/.ssh/authorized_keys
ip -4 addr show wlan0 | grep inet
whoami
```

De volta no **PC**, num repositório qualquer com um commit:

```bash
git remote add droid ssh://<USUARIO_DO_CELULAR>@<IP_DO_CELULAR>:8022/poc-app.git
GIT_SSH_COMMAND="ssh -i ~/.ssh/droidpaas_poc -o IdentitiesOnly=yes" git push droid HEAD:main
```
Esperado: o mesmo `remote:` ao vivo do bloco 4.

## 7. Limpeza (opcional; mantenha o `sshd` se for seguir para o 1G)

```bash
kill %1 2>/dev/null; rm -f ~/poc/run/t.sock
cp ~/.ssh/authorized_keys.bak ~/.ssh/authorized_keys 2>/dev/null || : > ~/.ssh/authorized_keys
```

## 8. Saída do bloco

| Item | Linha do `poc-report.md` |
|---|---|
| `sshd` sobe na 8022? `command=` bloqueia `ls` e shell? app inexistente/traversal bloqueados? push funciona com `remote:` ao vivo? | V-06 |
| `git-http-backend` existe e responde ao `info/refs`? | V-07 |
| Socket `0600` criado em `$HOME`? Hook (rodando sob o `sshd`) alcançou o socket? | V-12 |
| Push do PC funcionou (se feito)? IP/porta/usuário usados | V-06 |

Marque 1D no [README](README.md) e siga para o 1E (Go no Termux: SQLite e DNS).
