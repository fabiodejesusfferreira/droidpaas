# Bloco 1B — Supervisão (runit) e proxy (Caddy)

Cobre as atividades 1.3 e 1.4 e as hipóteses **V-02** (runsvdir detecta serviços), **V-04** (Caddy via Admin API), **V-09** (`chpst -n`, `svlogd`) e **V-10** (`/proc`). Reproduz à mão o que o `droidd` fará na Fase 2: criar serviço de forma atômica, supervisionar, logar e rotear por `Host`.

> Rode tudo no Termux do M22. Os caminhos usam `$PREFIX`/`$HOME`, que já existem lá. Se algo falhar, **copie a mensagem de erro** para o `poc-report.md`: falha também é resultado.

## 0. Pré-requisitos

```bash
pkg install -y curl procps   # curl para os testes; procps só se `ps -o` não funcionar
mkdir -p ~/poc/app ~/poc/env ~/poc/logs/poc-app
echo "SVDIR=$SVDIR"           # esperado: $PREFIX/var/service
pgrep -a runsvdir              # runsvdir precisa estar rodando
```

Se `SVDIR` vier vazio ou não houver `runsvdir`, **feche e reabra o Termux** (o `termux-services` sobe o `runsvdir` pelo `profile.d` ao iniciar a sessão). Se ainda não subir, anote: é o caso V-02 falho (plano B: `runsvdir` dedicado, ADR-03).

## 1. App de teste

```bash
cat > ~/poc/app/server.js <<'EOF'
const http = require("http");
const port = process.env.PORT || 20001;
http.createServer((req, res) => {
  if (req.url === "/health") return res.end("ok");
  if (req.url === "/env") return res.end(`FOO=${process.env.FOO}\n`);
  res.end("hello from poc-app\n");
}).listen(port, process.env.HOST || "127.0.0.1", () => console.log("listening on " + port));
EOF
printf '20001' > ~/poc/env/PORT
printf '127.0.0.1' > ~/poc/env/HOST
printf 'bar' > ~/poc/env/FOO
```

Um arquivo por variável, sem quebra de linha: é o formato do envdir (ADR-07).

## 2. Serviço runit criado de forma atômica (V-02, V-09)

O diretório nasce com prefixo `.` (o `runsvdir` ignora nomes iniciados em ponto) e com o arquivo `down`; só então é renomeado.

```bash
SV=$PREFIX/var/service
T=$SV/.dp-tmp-poc-app
mkdir -p $T/log

cat > $T/run <<EOF
#!$PREFIX/bin/sh
exec 2>&1
export PATH=$PREFIX/bin:\$PATH
cd $HOME/poc/app || exit 1
exec chpst -e $HOME/poc/env -n 10 node server.js
EOF

cat > $T/log/run <<EOF
#!$PREFIX/bin/sh
exec svlogd -tt $HOME/poc/logs/poc-app
EOF
printf 's1000000\nn5\n' > ~/poc/logs/poc-app/config

chmod +x $T/run $T/log/run
touch $T/down
start=$(date +%s)   # marcado imediatamente antes do mv, para medir só a detecção
mv $T $SV/poc-app
```

### 2.1 Medir o tempo de detecção (V-02)

Rode logo em seguida, no mesmo colar:

```bash
for i in $(seq 1 30); do
  [ -e $SV/poc-app/supervise/ok ] && break
  sleep 1
done
echo "detectado em $(( $(date +%s) - start )) s"
sv status poc-app   # esperado: "down: poc-app: ..." (por causa do arquivo down)
```

Meta do TSD: poucos segundos (o `runsvdir` varre a cada ~5 s). Se passar de 15 s ou nunca aparecer `supervise/ok`, V-02 falha.

### 2.2 Subir e testar

```bash
rm $SV/poc-app/down
sv up poc-app
sleep 2
sv status poc-app                       # run: poc-app: (pid N) Ns
curl -s http://127.0.0.1:20001/health   # ok
curl -s http://127.0.0.1:20001/env      # FOO=bar  (envdir funcionando)
```

### 2.3 `chpst -n` e logs (V-09)

```bash
PID=$(cat $SV/poc-app/supervise/pid)
ps -o pid,ni,args -p $PID               # NI deve ser 10 (ou próximo; veja a nota abaixo)
cat ~/poc/logs/poc-app/current          # linha "listening on 20001" com timestamp
```

Nota: o `chpst` faz `exec`, então o PID do serviço já é o do `node`. Se `ps` mostrar `NI` ≠ 10, confira também `cut -d' ' -f19 /proc/$PID/stat` e isole o `chpst`:

```bash
chpst -n 10 sh -c 'cut -d" " -f19 /proc/$$/stat'   # esperado: 10
nice -n 10 sh -c 'cut -d" " -f19 /proc/$$/stat'
```

Anote o que o Android entrega (alguns kernels/SELinux limitam `nice`).

> **Dica ao colar:** no terminal do celular, cole **um bloco por vez** e espere o prompt voltar. Se aparecer `>` no início da linha, o shell está esperando fechar um bloco: `Ctrl+C` e recomece.

### 2.4 Reinício automático e parada (RNF-06)

```bash
OLD=$(cat $SV/poc-app/supervise/pid)
kill -9 $OLD
sleep 1; sv status poc-app
sleep 4
NEW=$(cat $SV/poc-app/supervise/pid); echo "pid: $OLD -> $NEW"
curl -s http://127.0.0.1:20001/health
sv -w 10 stop poc-app    # TERM, espera até 10 s
sv status poc-app        # down
sv up poc-app
```

Meta: o app volta em ≤ 5 s (RNF-06).

## 3. `/proc` (V-10)

```bash
PID=$(cat $SV/poc-app/supervise/pid)
grep VmRSS /proc/$PID/status
cut -d' ' -f14,15 /proc/$PID/stat    # utime e stime
head -1 /proc/stat
cat /proc/loadavg
grep -E 'MemTotal|MemAvailable' /proc/meminfo
```

Anote qual desses comandos dá "Permission denied" (impacta RF-42 e o `measure.sh`).

## 4. Caddy via Admin API (V-04)

### 4.1 Bootstrap

```bash
mkdir -p ~/poc/caddy
cat > ~/poc/caddy/bootstrap.json <<'EOF'
{
  "admin": { "listen": "127.0.0.1:2019" },
  "apps": { "http": { "servers": { "dp": {
    "listen": ["127.0.0.1:8080"],
    "automatic_https": { "disable": true },
    "routes": [ { "handle": [
      { "handler": "static_response", "status_code": "404",
        "body": "DroidPaaS: nenhum app para este host." } ] } ]
  } } } }
}
EOF
```

Suba o Caddy **como serviço runit** (mesmo caminho do `dp-caddy` da Fase 2):

```bash
T=$SV/.dp-tmp-poc-caddy
mkdir -p $T/log ~/poc/logs/poc-caddy
cat > $T/run <<EOF
#!$PREFIX/bin/sh
exec 2>&1
export PATH=$PREFIX/bin:\$PATH
export HOME=$HOME
exec caddy run --config $HOME/poc/caddy/bootstrap.json
EOF
cat > $T/log/run <<EOF
#!$PREFIX/bin/sh
exec svlogd -tt $HOME/poc/logs/poc-caddy
EOF
chmod +x $T/run $T/log/run
mv $T $SV/poc-caddy
sleep 8
sv status poc-caddy
tail -20 ~/poc/logs/poc-caddy/current
curl -si http://127.0.0.1:8080/ | head -5     # 404 com o texto do DroidPaaS
```

Se o Caddy não subir, a causa costuma estar no `tail` (diretório de dados/DNS/permissão). **Cole o erro no relatório.**

### 4.2 Config completa via `POST /load` (ADR-05)

```bash
cat > ~/poc/caddy/full.json <<'EOF'
{
  "admin": { "listen": "127.0.0.1:2019" },
  "apps": { "http": { "servers": { "dp": {
    "listen": ["127.0.0.1:8080"],
    "automatic_https": { "disable": true },
    "routes": [
      { "match": [{ "host": ["poc.local"] }],
        "handle": [{ "handler": "reverse_proxy", "upstreams": [{ "dial": "127.0.0.1:20001" }] }],
        "terminal": true },
      { "handle": [{ "handler": "static_response", "status_code": "404",
        "body": "DroidPaaS: nenhum app para este host." }] }
    ]
  } } } }
}
EOF
curl -s -o /dev/null -w "load: %{http_code}\n" -X POST -H 'Content-Type: application/json' \
  --data @$HOME/poc/caddy/full.json http://127.0.0.1:2019/load

curl -s -H 'Host: poc.local' http://127.0.0.1:8080/health      # ok
curl -s -H 'Host: poc.local' http://127.0.0.1:8080/env         # FOO=bar
curl -si -H 'Host: outro.local' http://127.0.0.1:8080/ | head -1   # 404
```

### 4.3 Rota estática e app parado (503)

```bash
mkdir -p ~/poc/static && echo '<h1>estatico</h1>' > ~/poc/static/index.html
```

Cole **um bloco só** (o arquivo completo; não edite o anterior). O `$HOME` é expandido na hora porque o `EOF` não tem aspas:

```bash
cat > ~/poc/caddy/full2.json <<EOF
{
  "admin": { "listen": "127.0.0.1:2019" },
  "apps": { "http": { "servers": { "dp": {
    "listen": ["127.0.0.1:8080"],
    "automatic_https": { "disable": true },
    "routes": [
      { "match": [{ "host": ["poc.local"] }],
        "handle": [{ "handler": "reverse_proxy", "upstreams": [{ "dial": "127.0.0.1:20001" }] }],
        "terminal": true },
      { "match": [{ "host": ["blog.local"] }],
        "handle": [{ "handler": "file_server", "root": "$HOME/poc/static" }],
        "terminal": true },
      { "match": [{ "host": ["parado.local"] }],
        "handle": [{ "handler": "static_response", "status_code": "503", "body": "DroidPaaS: app parado." }],
        "terminal": true },
      { "handle": [{ "handler": "static_response", "status_code": "404",
        "body": "DroidPaaS: nenhum app para este host." }] }
    ]
  } } } }
}
EOF
curl -s -o /dev/null -w "load: %{http_code}\n" -X POST -H 'Content-Type: application/json' \
  --data @$HOME/poc/caddy/full2.json http://127.0.0.1:2019/load
curl -s -H 'Host: blog.local' http://127.0.0.1:8080/                  # <h1>estatico</h1>
curl -si -H 'Host: parado.local' http://127.0.0.1:8080/ | head -1     # 503
```

### 4.4 Caddy sobrevive ao "droidd" e reaplica config

```bash
sv restart poc-caddy; sleep 6
curl -si -H 'Host: poc.local' http://127.0.0.1:8080/health | head -1
```

Após o restart, a rota `poc.local` **não** deve existir (voltou ao bootstrap): é o motivo pelo qual o `droidd` reaplica a config ao detectar o Caddy reiniciado (§8.5.3). Reaplique com o `POST /load` e confirme.

## 5. Limpeza (opcional, mantenha se for usar no 1C/1D)

```bash
sv down poc-app poc-caddy
rm -rf $SV/poc-app $SV/poc-caddy
```

## 6. Saída do bloco

| Item | Linha do `poc-report.md` |
|---|---|
| Tempo de detecção do serviço novo (§2.1), `supervise/ok` aparece? | V-02 |
| `sv up/stop/restart`, tempo de reinício após `kill -9` | V-02 / RNF-06 (nota) |
| `chpst -n` aplicou nice? `chpst -e` injetou `FOO`? `svlogd` gravou/rotacionou? | V-09 |
| Quais leituras de `/proc` funcionaram | V-10 |
| Caddy subiu como serviço? `POST /load` aceito? roteou por `Host`? 404/503/estático? | V-04 |
| Erros de DNS/diretório/permissão do Caddy | Decisões alteradas |

Marque 1B no checklist de [README.md](README.md) e siga para o 1C (`cloudflared` modo token e wildcard).
