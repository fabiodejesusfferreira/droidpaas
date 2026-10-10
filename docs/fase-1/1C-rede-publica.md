# Bloco 1C — Rede pública (`cloudflared`)

Cobre a atividade 1.5, a hipótese **V-05** e a questão **Q-03** (como entregar o token sem aparecer no `ps`).

**Situação:** sem domínio na Cloudflare não dá para testar o modo token com hostname wildcard (ADR-06). Este bloco tem duas partes:

- **Parte A (agora):** *quick tunnel* (`trycloudflare.com`), que não exige conta nem domínio. Prova que o `cloudflared` roda no Termux, sob runit, e que o tráfego público chega ao Caddy e ao app. **Não** prova token nem wildcard.
- **Parte B (quando houver domínio):** modo token + wildcard. Fica **pendente**; V-05 só é fechada aqui.

> Os serviços `poc-app` (porta 20001) e `poc-caddy` (8080) do bloco 1B precisam estar rodando. Confira com `sv status poc-app poc-caddy`.

## Parte A — Quick tunnel

### A.1 Rota do Caddy para o host do túnel

O quick tunnel entrega o `Host` como `<palavras-aleatórias>.trycloudflare.com`. Um host com curinga cobre qualquer um:

```bash
cat > ~/poc/caddy/full3.json <<EOF
{
  "admin": { "listen": "127.0.0.1:2019" },
  "apps": { "http": { "servers": { "dp": {
    "listen": ["127.0.0.1:8080"],
    "automatic_https": { "disable": true },
    "routes": [
      { "match": [{ "host": ["poc.local", "*.trycloudflare.com"] }],
        "handle": [{ "handler": "reverse_proxy", "upstreams": [{ "dial": "127.0.0.1:20001" }] }],
        "terminal": true },
      { "handle": [{ "handler": "static_response", "status_code": "404",
        "body": "DroidPaaS: nenhum app para este host." }] }
    ]
  } } } }
}
EOF
```

```bash
curl -s -o /dev/null -w "load: %{http_code}\n" -X POST -H 'Content-Type: application/json' \
  --data @$HOME/poc/caddy/full3.json http://127.0.0.1:2019/load
```

### A.2 Serviço runit do túnel

```bash
SV=$PREFIX/var/service
T=$SV/.dp-tmp-poc-cloudflared
mkdir -p $T/log ~/poc/logs/poc-cloudflared
cat > $T/run <<EOF
#!$PREFIX/bin/sh
exec 2>&1
export PATH=$PREFIX/bin:\$PATH
exec cloudflared tunnel --no-autoupdate --url http://127.0.0.1:8080
EOF
cat > $T/log/run <<EOF
#!$PREFIX/bin/sh
exec svlogd -tt $HOME/poc/logs/poc-cloudflared
EOF
chmod +x $T/run $T/log/run
mv $T $SV/poc-cloudflared
sleep 15
sv status poc-cloudflared
```

### A.3 Obter a URL e testar

```bash
grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' ~/poc/logs/poc-cloudflared/current | tail -1
tail -15 ~/poc/logs/poc-cloudflared/current
```

Com a URL (`https://xxxx.trycloudflare.com`), teste **de fora do aparelho**: no PC ou no celular **em dados móveis** (desligue o Wi-Fi), para provar que o caminho é pelo túnel:

```bash
curl -si https://xxxx.trycloudflare.com/health      # esperado: HTTP 200 e corpo "ok"
curl -s  https://xxxx.trycloudflare.com/env         # esperado: FOO=bar
```

Se o DNS do nome novo demorar, espere 1–2 min e repita. Se `cloudflared` falhar por DNS/TLS no Termux, **cole o erro**: é a causa C-07/R-13.

### A.4 Resiliência (opcional, rápido)

```bash
sv restart poc-cloudflared; sleep 15
grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' ~/poc/logs/poc-cloudflared/current | tail -1
```

Anote se a URL **muda** após o restart (quick tunnels mudam). Por isso o DroidPaaS usa túnel nomeado com token.

### A.5 Diagnóstico de um 502

Um `502` com página da Cloudflare significa que o túnel existe, mas o `cloudflared` não obteve resposta do Caddy. Teste de dentro para fora, sempre com a URL **lida do log**, não digitada:

```bash
URL=$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' ~/poc/logs/poc-cloudflared/current | tail -1); echo $URL
H=${URL#https://}
curl -si -H "Host: $H" http://127.0.0.1:8080/health | head -1     # Caddy local com o host do túnel: esperado 200
curl -si $URL/health | head -1                                    # pelo túnel (do próprio celular)
grep -i -E 'error|unable|origin' ~/poc/logs/poc-cloudflared/current | tail -5
```

- Primeira linha `404` → a rota `*.trycloudflare.com` não está carregada no Caddy (reaplique o `full3.json`, o Caddy pode ter reiniciado).
- Primeira `200` e segunda `502` → problema entre `cloudflared` e o Caddy: cole as linhas do `grep`.
- Ambas `200` → o 502 anterior era de uma URL antiga/errada; teste então de fora do aparelho com `$URL`.

## Q-03 — Entrega do token (dá para testar sem domínio)

```bash
cloudflared tunnel run --help 2>&1 | grep -i -E 'token|TUNNEL_TOKEN'
```

Anote quais formas existem: `--token`, `--token-file`, variável `TUNNEL_TOKEN`. O desenho preferido é a variável via **envdir** (`chpst -e`), que não aparece no `ps` e já é o padrão do ADR-07. Confirme na Parte B que a variável funciona e que o `ps` não mostra o token:

```bash
ps -A -o pid,args | grep cloudflared    # o token NÃO deve aparecer (sem -A o ps do Termux só lista o terminal atual)
```

## Parte B — Modo token + wildcard (pendente: exige domínio)

Requisitos: conta Cloudflare (grátis) com um domínio cujo DNS esteja na Cloudflare.

1. Zero Trust → Networks → Tunnels → **Create a tunnel** (Cloudflared). Copie o **token**.
2. Public hostname: `*.apps.<seu-domínio>` → `HTTP` → `127.0.0.1:8080`. Se o painel recusar o wildcard, crie o CNAME `*.apps` → `<id>.cfargotunnel.com` no DNS e registre a recusa (cai no plano B do TSD: um hostname por app).
3. Entregue o token pelo envdir:
   ```bash
   mkdir -p ~/poc/cf-env && printf '%s' '<TOKEN>' > ~/poc/cf-env/TUNNEL_TOKEN && chmod 600 ~/poc/cf-env/TUNNEL_TOKEN
   ```
   e no `run`: `exec chpst -e $HOME/poc/cf-env cloudflared tunnel --no-autoupdate run`.
4. Adicione `"*.apps.<seu-domínio>"` ao `host` da rota do Caddy e teste `https://qualquer.apps.<seu-domínio>/health`.
5. **SSE:** quick tunnels não suportam Server-Sent Events. Com o túnel nomeado, teste uma resposta `text/event-stream` (a Fase 3 depende disso para os logs ao vivo).

## Limpeza

```bash
sv down poc-cloudflared; rm -rf $PREFIX/var/service/poc-cloudflared
```

## Saída do bloco

| Item | Linha do `poc-report.md` |
|---|---|
| `cloudflared` roda sob runit; URL gerada; `/health` e `/env` acessíveis de fora | V-05 (parcial) |
| Erros de DNS/TLS no Termux | Decisões alteradas / V-13 |
| Formas de passar o token e resultado do `ps` | Q-03 |
| Wildcard aceito? SSE passa pelo túnel nomeado? | V-05 (conclusão, Parte B) |
