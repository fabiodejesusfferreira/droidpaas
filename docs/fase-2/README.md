# Fase 2 — MVP sem painel

**Objetivo:** `droidd` + `droid` (CLI) + API REST: deploy por git, build, runit, Caddy, túnel, env vars, logs. Só começa após o gate da Fase 1. Detalhes: TSD §16.2.

## Sub-fases

- ☑ 2A Fundação: config, slog, SQLite + migrações, `droidd init/serve` → AT-01 (ver resultado abaixo)
- ☑ 2B API base, auth por token, eventos, socket Unix
- ☑ 2C Runit + modelo de apps, portas, envdir cifrado → AT-08 (a parte de apps/envdir validada com `sv`/`chpst` reais)
- ☑ 2D Proxy Caddy + páginas 404/503 → AT-12 (rota estática testada na 2E, quando houver release)
- ☑ 2E Build, deploy, rollback (todos os runtimes) → AT-04, AT-05, AT-06
- ☐ 2F Git SSH + hook → AT-03
- ☐ 2G Reconciliador, crash loop, logs de runtime → AT-09, AT-10
- ☐ 2H CLI `droid` + runtimes python/go/custom → AT-07
- ☐ 2I Túnel, Git HTTP, backup, docs, soak 72 h → AT-11

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
