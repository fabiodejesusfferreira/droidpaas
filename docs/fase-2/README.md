# Fase 2 — MVP sem painel

**Objetivo:** `droidd` + `droid` (CLI) + API REST: deploy por git, build, runit, Caddy, túnel, env vars, logs. Só começa após o gate da Fase 1. Detalhes: TSD §16.2.

## Sub-fases

- ☑ 2A Fundação: config, slog, SQLite + migrações, `droidd init/serve` → AT-01 (ver resultado abaixo)
- ☑ 2B API base, auth por token, eventos, socket Unix
- ☑ 2C Runit + modelo de apps, portas, envdir cifrado → AT-08 (a parte de apps/envdir validada com `sv`/`chpst` reais)
- ☑ 2D Proxy Caddy + páginas 404/503 → AT-12 (rota estática testada na 2E, quando houver release)
- ☑ 2E Build, deploy, rollback (todos os runtimes) → AT-04, AT-05, AT-06
- ☑ 2F Git SSH + hook → AT-03
- ☑ 2G Reconciliador, crash loop, logs de runtime → AT-09, AT-10
- ☑ 2H CLI `droid` + runtimes python/go/custom → AT-07
- ☑ 2I Túnel, Git HTTP, backup, docs, soak 72 h e AT-11 → AT-11 (RF-16 com domínio: pendência não bloqueante, ver abaixo)

## Gate

AT-01…AT-12 no M22 · RNF-01/02 medidos · soak de 72 h sem mortes não intencionais · TSD v0.3.

## Decisões e resultado

### 2A — Fundação (concluída em 2026-10-12)

**Entregue:** `internal/config` (TOML estrito, defaults do TSD v0.2, `~`/`$PREFIX`), `internal/store` (SQLite WAL, migrações embutidas em `migrations/`, backup `VACUUM INTO` antes de migrar um banco com dados, rollback de migração que falha), `internal/auth` (argon2id, tokens `dp_…` com SHA-256), `internal/api` (`/healthz` e `/internal/ping`), `droidd init` e `droidd serve`.

**Testes (`make test`):** config (defaults, override, campos desconhecidos, validação), auth (senha, token), store (12 tabelas, pragmas, idempotência, backup, rollback, `ON DELETE CASCADE`, interrupção de deploys) e `init` de ponta a ponta (AT-01 automatizado: permissões `0600`/`0700`, token exibido uma vez, segunda execução idempotente, senha curta/divergente rejeitada).

**Medido no M22** (binário compilado no aparelho, `go build` em 55 s com o cache aquecido):

| Meta | Resultado |
|---|---|
| RNF-01 RSS do `droidd` ocioso ≤ 40 MB | **9,6 MB** |
| RNF-02 CPU ocioso < 1 % | **0,00 %** (0 ticks em 60 s) |
| argon2id ≤ ~500 ms (§13.2) | `TestPassword` (1 hash + 2 verificações) em 0,51 s, ≈ 170 ms por operação |
| `droidd init` em ambiente limpo (AT-01) | OK: estrutura, `droidd.toml` `0600`, `master.key` 32 bytes `0600`, banco, admin e token |
| Socket Unix | `0600`; `ping` OK; `SIGTERM` encerra e remove o socket |

**Desvios do plano/TSD:**
- `go.mod` exige **Go 1.26** (`modernc.org/sqlite` v1.60.1); o Go do PC baixa o toolchain sozinho e o do M22 (1.27.1) atende.
- O pacote `migrations/` tem um `migrations.go` com `go:embed` (um `embed` não alcança diretórios acima do pacote).
- **A criação dos serviços runit `droidd` e `dp-caddy` pelo `init` ficou para a 2C**, que traz o `ServiceManager` real. O `init` avisa disso no checklist.
- O `init` só escreve `~/.termux/boot/droidpaas` quando `$PREFIX` existe e o arquivo não existe; nunca altera outros scripts de boot.
- Portas padrão do proxy já seguem o TSD v0.2 (`18080`/`12019`).


### 2B — API base (concluída em 2026-10-12)

`internal/api` (`Server`, auth Bearer com escopos `full`/`git`, erro JSON padronizado, JSON estrito, paginação `limit`/`before`), `internal/events`, `internal/crypt` (AES-256-GCM com `master.key`). Endpoints: `/healthz`, `/api/v1/me`, `/tokens` (GET/POST/DELETE), `/settings` (GET/PUT) e `PUT /settings/tunnel` (token cifrado, nunca devolvido), `/events`.

**Desvios:** migração `0002_scope_crash.sql` adiciona `api_tokens.scope` (o DDL do TSD não tinha a coluna dos escopos do §13.2) e `apps.crashed_at` (o estado `crashed` precisa sobreviver a um restart do `droidd`, senão o reconciliador religaria o app em loop). `last_used_at` do token é gravado no máximo 1×/min. SQLite com `synchronous=NORMAL` (recomendado com WAL).

### 2C — Runit e modelo de apps (concluída em 2026-10-12)

`internal/runit` (`ServiceManager` real via `sv` + `Fake`, geradores de `run`/`log/run`/`start.sh`, `EncodeEnvValue`), `internal/apps` (criar/editar/remover, portas, start/stop/restart, estado observado, domínios automáticos por `base_domain`, variáveis cifradas + envdir), `store` (apps, domínios, env, releases) e a API `/apps`, `/apps/{name}/env`, `/apps/{name}/domains`, `start|stop|restart`.

**Teste no aparelho** (`go test -tags termux ./internal/runit`, com `runsvdir` próprio em diretório temporário): criação atômica + detecção pelo `runsvdir` em **1,2 s**; `kill -9` → novo PID em **1,04 s** (RNF-06 ≤ 5 s); `Down`, `Restart` e `Remove` OK, sem ressuscitar o serviço; o `run` gerado entrega ao app, via `chpst -e` real, valores com quebra de linha, vazios e com aspas/`$`/`=`.

**Achados e desvios:**
- **`chpst` não executa scripts com shebang no Termux** (`unable to run: … file does not exist`; binários funcionam). O TSD A.1 (`exec chpst … start.sh`) foi corrigido para `exec chpst -e … -n N sh start.sh`.
- **envdir:** só a primeira linha vale e arquivo vazio apaga a variável, então `\n` vira NUL no arquivo (o `chpst` converte de volta) e valor vazio é gravado como `\n`. Espaços/tabs finais são cortados pelo `chpst` (limitação conhecida).
- Variáveis reservadas (rejeitadas na API): `PORT`, `HOST`, `NODE_ENV`, `DP_*`.
- `releases.runtime` (migração `0003`) guarda o runtime resolvido da release (NODE_ENV, start e rollback).
- O `umask` do Termux é `077`: os testes não assumem modos exatos de arquivos de serviço.
- Revelar um segredo é `GET /apps/{name}/env/{key}` (auditado, sem registrar o valor).

### 2D — Proxy Caddy (concluída em 2026-10-12)

`internal/proxy`: `Generate` (config completa e determinística: painel opcional → apps por nome → fallback 404; golden file em `testdata/golden.json`, validado com `caddy validate` no aparelho), `Client` (`POST /load`), `Applier` (hash da config, backoff 2 s…60 s, `Invalidate`), `BuildInput` (estado desejado → rotas: só roteia para app `running`, com release e sem crash loop; release `static` vira `file_server` em `current/<static_dir>`), `Controller` (passo do reconciliador: garante `dp-caddy`, grava `proxy/caddy.json`, reaplica quando o Caddy reinicia) e páginas 404/503 embutidas (sobrescrevíveis em `proxy/pages/`). `internal/reconcile` ganhou o laço base (`Loop`, `Wake`).

**Decisão:** o `dp-caddy` sobe com `caddy run --config proxy/caddy.json`, arquivo que o `droidd` regrava a cada mudança. Assim o Caddy volta com as rotas certas depois de um restart ou de um reboot **mesmo sem o droidd** (RNF-08). Também usa `XDG_CONFIG_HOME`/`XDG_DATA_HOME` sob `$DP_HOME/proxy` e `GOMEMLIMIT=128MiB` (no PoC o Caddy gravava em `~/caddy` e `~/.config/caddy`).

**Teste no aparelho** (`test/e2e/2d-proxy.sh`, sandbox com `runsvdir` próprio, prefixo `dpt-` e portas 18090/28080/22019; 9 verificações): host desconhecido → 404; app sem release → 503; `kill -9` no Caddy → ele volta servindo a mesma rota; `kill -9` no `droidd` → proxy segue servindo 503/404.

### 2E — Build, deploy e rollback (concluída em 2026-10-12)

`internal/system` (`ExecRunner` com grupo de processos + SIGTERM→SIGKILL, `GitCLI` com extração de tar segura contra `..`, caminhos absolutos e symlinks, relógios), `internal/build` (detecção na ordem do §8.3.1, `droidpaas.toml` estrito, Procfile, planos de node/python/go/static/custom, plano salvo na release), `internal/deploy` (fila persistente com serialização por app, estágios `fetch→detect→install→build→prepare→switch→start→health→finalize`, health TCP/HTTP, rollback automático, rollback manual, retenção, cancelamento, recuperação pós-queda, log ao vivo com replay e retomada por número de linha) e a API `/apps/{name}/deploys|rollback|releases`, `/deploys/{id}`, `/deploys/{id}/logs` (texto ou SSE com `Last-Event-ID` e heartbeat) e `/deploys/{id}/cancel`.

**Teste no aparelho** (`test/e2e/2e-deploy.sh`, 22 verificações, sandbox com runit e Caddy reais): deploy de um app Node em **11 s** (RNF-05 ≤ 90 s); v2 servida pelo Caddy; build quebrado → `failed` e a v2 segue no ar (AT-04/RNF-09); app que sai ao subir → rollback automático e v2 de volta (AT-05); rollback manual para a v1 (AT-06); variável + "salvar e reiniciar", segredo mascarado na API e visível ao app (AT-07); `kill -9` → novo PID em < 1 s (AT-08); site estático servido pelo Caddy sem processo (AT-12).

**Desvios e decisões:**
- Migração `0004` adiciona `git_sha`, `git_ref` e `rebuild` a `deploys`: o DDL do TSD não tinha onde guardar o que a fila deve construir.
- **Q-05 decidida: só `npm`** (com `npm ci` se houver `package-lock.json`, senão `npm install` com aviso). `pnpm`/`yarn`/`bun` ficam fora da Fase 2.
- O build instala com `NODE_ENV=development` e `--max-old-space-size=512`, depois roda `npm prune --omit=dev`; o app roda com `NODE_ENV=production` (PoC 1F).
- O build enxerga as variáveis de ambiente do app (necessário para `NEXT_PUBLIC_*` etc.); `PORT`/`HOST`/`DP_*` só existem em runtime.
- Deploy num app **parado** ativa a release mas não inicia o processo. Deploy num app estático para o processo da release anterior, se havia.
- O `static_dir` efetivo (app > `droidpaas.toml`) fica no plano salvo da release e o proxy o lê de lá.
- Cancelar só é possível antes da troca de release (`409 too_late` depois).

### 2F — Git por SSH e hook (concluída em 2026-10-12)

`internal/gitsrv` (repo bare + hook com caminhos absolutos, `ParseShellCommand` estrito, bloco gerenciado `# BEGIN/END DROIDPAAS` no `authorized_keys`, validação de chave pública sem opções), `internal/client` (cliente HTTP por socket Unix + `PostReceive`), `droid git-shell` e `droid hook post-receive`, `POST /internal/hook/post-receive` (log do deploy em streaming; exit code no trailer `X-Droid-Exit`), `GET/POST/DELETE /ssh-keys` e o passo `git-keys` do reconciliador (regrava o bloco ao subir). `droidd init` agora linka `droidd` e `droid` em `$DP_HOME/bin`.

**Teste no aparelho** (`test/e2e/2f-git.sh`, 29 verificações, com um `sshd` próprio do sandbox na porta 28022): `git push` real por SSH mostra `remote: DroidPaaS: deploy #1 … succeeded` e o app responde pelo hostname (AT-03); build quebrado → o push é aceito, o erro do build aparece no terminal e a v1 segue no ar (AT-04); push em outro branch não dispara deploy; `ls`, shell interativo, app inexistente, `../../etc.git` e chave não cadastrada são negados; remover a chave pela API limpa o `authorized_keys` e fecha o acesso.

**Decisões:** o post-receive nunca desfaz o push (o commit já foi recebido); o exit code do hook reflete o deploy. Se o droidd estiver fora do ar o hook avisa e manda rodar `droid deploy`. O `git-shell` exporta `DROID_KEY_ID` para o hook. `ssh_port` passou a ser configurável (`git.ssh_port`, padrão 8022) e aparece na URL do remote.

### 2G — Reconciliador, crash loop e logs de runtime (concluída em 2026-10-12)

`internal/reconcile` (`Loop` + passos `domains`, `git-keys`, `apps`, `proxy` e `logs`; `CrashTracker` com janela deslizante; remoção de serviços órfãos só depois de 3 ciclos sem dono; o passo de apps não compete com um deploy em andamento), `internal/logs` (`Tail` atravessando a rotação do svlogd e `Follow` por polling de 500 ms que sobrevive à rotação sem perder linhas) e `GET /apps/{name}/logs?tail=&follow=1` (texto ou SSE `line` com `ts`/`text`).

**Teste no aparelho** (`test/e2e/2g-reconcile.sh`, 24 verificações): `kill -9` no `droidd` → app e Caddy mantêm o mesmo PID e continuam respondendo; ao voltar, o `droidd` retoma o estado sem reiniciar o app (AT-10/RNF-08); tail e follow trazem o stdout do app (linha nova em ≤ 2 s); `kill -9` repetido → `crashed`, serviço parado, proxy 503, evento `app.crash_loop` com as últimas linhas do log, sem reinício infinito, e `Start` tira do estado (AT-09); serviço órfão removido após o período de graça.

**Bugs reais encontrados pelo e2e (e corrigidos):**
- **Apps iniciados com `npm start` deixam o `node` órfão se o `npm` levar SIGKILL** (o filho segura a porta e o próximo start falha com `EADDRINUSE`). O planejador agora usa `node <arquivo>` direto quando o script `start` é simples, o que também economiza a RAM do processo `npm`; scripts compostos continuam via `npm start` (limitação documentada).
- O roteiro e2e derrubava os serviços com `sv exit` (que **não** para o app) e acumulava processos órfãos que seguravam portas e **mascaravam falhas**; o teardown agora faz `sv down` antes e mata processos antigos do sandbox. Além disso, `wait_until test "$(…)"` avaliava a condição uma única vez; a função passou a reavaliar uma expressão a cada tentativa. As 4 suítes foram re-executadas depois dessas correções (2D: 9, 2E: 22, 2F: 29, 2G: 24 verificações, todas verdes).
- Um push SSH agora dispara deploy via hook; os testes que dirigem deploys pela API removem o hook do repo.

**Decisões:** o estado `crashed` é persistido (`apps.crashed_at`, migração `0002`) e só sai por ação explícita (Start/Restart/novo deploy). Reinício é detectado por troca de PID entre ciclos; com `reconcile.interval = 10 s` o limiar 5/60 s dispara depois de ~50 s de loop.

### 2H — CLI `droid` e runtimes (concluída em 2026-10-12)

`internal/client` (credenciais em `~/.config/droid/config.toml` 0600, `DROID_URL`/`DROID_TOKEN`/`DROID_CONFIG`, cliente REST com `APIError` e consumo de SSE) e `cmd/droid` com todos os comandos do §11: `login`, `apps [create|rm]`, `start|stop|restart`, `deploy [--ref --rebuild --no-follow]` (acompanha o log ao vivo e sai 0/1 pelo status), `rollback [--release]`, `logs [-f -n]`, `deploys`, `deploy-logs [-f]`, `env [list|set|unset|reveal]`, `domains`, `keys`, `tokens`, `tunnel set`, `status`, `version`, mais `--json` em qualquer posição. Flags podem vir antes ou depois dos argumentos. Códigos de saída: 0 sucesso, 1 erro, 2 uso incorreto, 3 autenticação. Novo `GET /api/v1/system` (versão, uptime, serviços base e contagem de apps por estado) e `resolved_runtime` no JSON de apps. `internal/api/apitest` sobe um droidd em memória para os testes da CLI.

**Testes:** 8 testes de integração da CLI (em processo, contra o servidor real em memória) + `test/e2e/2h-cli.sh` no aparelho (**44 verificações**): deploy de node com `npm run build` (11 s), python com venv+pip (19 s), go com `go build` (46 s), site estático e custom com Procfile, erro claro quando não há runtime detectável, e o uso diário só pela CLI (`login`, `deploy`, `env set --secret --restart`, `reveal`, `logs`, `deploy --rebuild`, `rollback`, `stop`/`start`, `apps rm`).

**Correções feitas aqui:** o estado de um site estático era `stopped` (o serviço runit fica parado de propósito); agora é `running` quando a release está ativa e o app deve rodar. `droid status` lista os serviços pelo nome real (prefixo configurável).

### 2I — Túnel, Git HTTP, backup e entrega (código e docs concluídos em 2026-10-13)

`internal/tunnel` (serviço `dp-cloudflared` em modo token: o token vai para `etc/tunnel-env/TUNNEL_TOKEN` 0600, lido pelo `chpst -e`, e **nunca** aparece no script nem no `ps`; o serviço some quando o token é removido; estado `connected/connecting/down` pelo log), Git smart HTTP em `/git/<app>.git/*` (Basic com token `git`/`full`, `git http-backend` via CGI, o log do deploy chega ao terminal do `git push`), `GET /api/v1/backup` + `droid backup [--with-key]`, serviço runit `droidd` criado pelo `droidd init` (`--no-service` para sandboxes), `scripts/install.sh`, `docs/operacao.md` e o roteiro do soak (`scripts/soak/`).

**Teste no aparelho** (`test/e2e/2i-delivery.sh`, 26 verificações): token do túnel em arquivo 0600/diretório 0700, ausente do script, do `ps -A` e do banco (cifrado); Git HTTP com token de escopo `git` (clone/push → deploy com `remote:` no terminal; token inválido recusado; token `git` não acessa a API); backup 0600 com segredos cifrados, `.tar.gz` com `master.key`, e **restauração**: o `droidd` sobe sobre o backup e decifra o segredo com a chave do backup; reinício do `droidd` preserva tudo.

**Correção importante:** reinícios feitos pelo próprio DroidPaaS (deploy, start/stop/restart, rollback) contavam como quedas do app no detector de crash loop e podiam marcá-lo `crashed` depois de vários deploys seguidos. Agora `apps.Service.Touch` zera o histórico do detector.

### Estado do gate da Fase 2

| Critério (§16.2) | Situação |
|---|---|
| Todos os RF "M" implementados; testes unitários/integração com fakes | ✅ `make test` (15 pacotes) |
| AT-02, 03, 04, 05, 06, 07, 08, 09, 10, 12 no aparelho | ✅ automatizados e verdes (2D 9 · 2E 22 · 2F 29 · 2G 24 · 2H 44 · 2I 26 · soak-smoke 10 = **164** checagens) |
| AT-01 `droidd init` em ambiente limpo | ✅ automatizado (PC e aparelho, sandbox); falta rodar `scripts/install.sh` na instalação real |
| AT-11 reboot do aparelho | ⏳ exige instalação real (o PoC já validou o mecanismo: Termux:Boot + runsvdir) |
| RNF-01/02 (`droidd` ≤ 40 MB e < 1 % CPU) | ✅ medido: 9,6 MB ocioso / 0 % (2A) e 18–19 MB com 4 apps + deploys (soak reduzido) |
| Soak de 72 h sem mortes não intencionais (RNF-10) | ⏳ `scripts/soak/soak.sh` pronto e validado em escala reduzida; precisa de 72 h na instalação real |
| RNF-16/túnel público com domínio (RF-16) | ☑ validado na Fase 3 (2026-10-17) com domínio na Cloudflare (ver docs/fase-3) |
| Documentação mínima publicada; TSD atualizado | ✅ `docs/operacao.md`, TSD v0.3 |

**Para fechar o gate** é preciso instalar de verdade no M22 (`sh scripts/install.sh`), rodar o soak por 72 h (`nohup bash scripts/soak/soak.sh &`, depois `python3 scripts/soak/summary.py`), fazer um reboot (AT-11) e, com um domínio, validar o túnel. Isso altera o `SVDIR` e o boot reais do aparelho, por isso depende de autorização explícita.

### Gate da Fase 2 — instalação real (2026-10-13)

**AT-01 na instalação real ✅** (`scripts/install.sh` no M22): estrutura criada, `master.key`/banco/`droidd.toml`/config do `droid` em `0600`, `etc/secrets` em `0700`, serviço runit `droidd` criado e `dp-caddy` criado por ele; RSS medido: `droidd` 13 MB, Caddy 35 MB (RNF-01 ≤ 40 MB ✅). Os apps do usuário (`caddy file-server :8080`, `painel.js`) seguiram no ar. Bug achado e corrigido nesta etapa: o `init` não expandia `$PREFIX` e por isso não criava o serviço (commit `2b052b7`).

**AT-11 reboot ✅** com um app `running`, um `stopped` e um `crashed` (crash loop real, detectado em 5 `kill -9`): depois do reboot o `droidd` e o `dp-caddy` voltaram sozinhos ~3 s depois do Termux:Boot (`boot.log` 01:32:32Z; `sv status` mostra 131 s às 01:34:46Z) — RNF-07 ≤ 60 s ✅; `a11run` voltou `running` (respondendo na porta 20000), `a11stop` continuou `stopped` e `a11crash` continuou `crashed` (estado persistido). Os três apps de teste foram removidos.

**Soak de 72 h ⏳ em andamento:** iniciado em `2026-10-13T01:35Z` (22:35 em Brasília de 12/10), término previsto `2026-10-16T01:35Z`, com `scripts/soak/soak.sh` (apps `soaknode`, `soakpy`, `soakgo`, `soaksite`; deploy a cada 4 h, restart a cada 3 h, `kill -9` a cada 8 h). Saídas em `~/soak/` no aparelho; resumo com `python3 ~/droidpaas-src/scripts/soak/summary.py`. Aparelho na tomada (91 %, 37 °C no início).

**Túnel público com domínio (RF-16) ☑:** validado na Fase 3 com `dejesusdevts.com` (ver docs/fase-3).

### Soak de 72 h — resultado (colhido em 2026-10-16)

Instalação real no M22 (`droidd` + `dp-caddy` + 4 apps de exemplo), de `2026-10-13T01:35Z` a `2026-10-16T01:37Z` (4 265 amostras de 60 s, 0 lacunas > 150 s). O usuário usou o aparelho normalmente e o tirou da tomada várias vezes (**86 % das amostras fora da tomada**; bateria 50–100 %); o `droidd` e o Caddy seguiram sem nenhum restart por **93 h 50 min** (o soak terminou com 72 h; o aparelho ficou ligado até a colheita). Dados brutos em [soak/](soak/).

| Critério / métrica | Resultado |
|---|---|
| **Mortes não intencionais** (RNF-10, critério: 0) | **0** — 8 `kill -9` e 23 restarts pedidos, todos recuperados; PIDs distintos por app: node 20, python 22, go 9, static 1 (todos explicados por deploys/restarts/kills pedidos) |
| Sondas HTTP pelo Caddy | **17 060 de 17 060 = 200** (0 falhas) |
| Latência via Caddy | p50 7 ms · p95 13 ms · máx 99 ms |
| Recuperação após `kill -9` (RNF-06, ≤ 5 s) | 55–67 ms (o runit reinicia na hora quando o app viveu > 1 s) |
| Deploys periódicos | 17/17 com sucesso (node e python, ~10–20 s cada) |
| RSS do `droidd` (RNF-01 ≤ 40 MB) | **17,1 → 17,1 MB** entre o primeiro e o último quarto; mín 12,4 · máx 19,1 MB — sem vazamento |
| CPU do `droidd` (RNF-02 < 1 %) | **0,68 %** de um núcleo (41 ticks/60 s) com 0 apps, medido na colheita |
| Temperatura da bateria | mín 21,6 · média 28,6 · p95 32,6 · **máx 37,9 °C** (< 42 °C) |
| RAM disponível | mín 433 MB (02:05Z de 13/10, na tomada, durante a fase de builds do início) · média 1 419 MB; nenhum OOM |
| Disco do `$DP_HOME` | 146 → 173 → 201 → 202 → 202 MB: cresce enquanto o cache e as 5 releases se enchem e **estabiliza** (retenção funciona) |
| Banco | `droidpaas.db` 112 KB; WAL de 3,2 MB (agora limitado por `journal_size_limit`) |
| Eventos | 24 deploys `succeeded`, 1 `app.crash_loop` (o app de teste do AT-11, esperado), 2 `proxy.apply_failed` |

**Achados da colheita (corrigidos):**
- Os 2 `proxy.apply_failed` foram `connection refused` na Admin API do Caddy **logo depois do start** (na instalação e no reboot). Era ruído: o Caddy já sobe com o `caddy.json` certo. Agora uma falha dentro dos primeiros 20 s de vida do Caddy não gera erro nem evento.
- 1 aviso `saída inesperada do sv: unable to open supervise/ok` ao criar um app (o `runsv` ainda não tinha assumido o serviço). Agora é `ErrNotReady` e o reconciliador espera o próximo ciclo.
- O WAL do SQLite ficou em 3,2 MB: passou a usar `journal_size_limit(16 MiB)`.
- O IP do aparelho mudou por DHCP durante o teste (192.168.1.7 → .4): para acesso remoto estável use um nome (Tailscale/mDNS) ou reserve o IP no roteador.
- O Go do aparelho foi atualizado de 1.27.1 para 1.27.2 sem impacto.

Regressão depois dessas correções, no aparelho: 2D 9 · 2E 22 · 2F 29 · 2G 24 · 2H 44 · 2I 26 verificações, todas verdes.

### Gate da Fase 2 — FECHADO (2026-10-16)

| Critério (§16.2) | Situação |
|---|---|
| Todos os RF "M" implementados; testes unitários/integração com fakes | ✅ |
| AT-01…AT-12 no M22 | ✅ (AT-01 e AT-11 na instalação real; os demais automatizados no aparelho) |
| RNF-01/02 medidos no aparelho | ✅ 17 MB / 0,68 % |
| Soak de 72 h sem mortes não intencionais (RNF-10) | ✅ 0 mortes em 72 h (93 h 50 min de uptime contínuo do `droidd`) |
| Documentação mínima publicada; TSD atualizado | ✅ `docs/operacao.md`, TSD v0.3 |

**Pendência não bloqueante:** RF-16 é "Should" (e V-05 Parte B). O plumbing do túnel está testado (token em arquivo 0600, fora do `ps`, estado `connected/down`), mas a validação ponta a ponta (wildcard, acesso público e SSE pelo túnel nomeado) exige um domínio na Cloudflare. Ela **precisa** acontecer antes de usar os logs ao vivo do painel por fora da rede local (Fase 3).
