# Fase 2 — MVP sem painel

**Objetivo:** `droidd` + `droid` (CLI) + API REST: deploy por git, build, runit, Caddy, túnel, env vars, logs. Só começa após o gate da Fase 1. Detalhes: TSD §16.2.

## Sub-fases

- ☑ 2A Fundação: config, slog, SQLite + migrações, `droidd init/serve` → AT-01 (ver resultado abaixo)
- ☑ 2B API base, auth por token, eventos, socket Unix
- ☐ 2C Runit + modelo de apps, portas, envdir cifrado → AT-08
- ☐ 2D Proxy Caddy + páginas 404/503 → AT-12
- ☐ 2E Build, deploy, rollback (node + static) → AT-04, AT-05, AT-06
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
