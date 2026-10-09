# Fase 2 — MVP sem painel

**Objetivo:** `droidd` + `droid` (CLI) + API REST: deploy por git, build, runit, Caddy, túnel, env vars, logs. Só começa após o gate da Fase 1. Detalhes: TSD §16.2.

## Sub-fases

- ☐ 2A Fundação: config, slog, SQLite + migrações, `droidd init/serve` → AT-01
- ☐ 2B API base, auth por token, eventos, socket Unix
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

_A preencher._
