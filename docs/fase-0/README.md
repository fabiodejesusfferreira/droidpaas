# Fase 0 — Bootstrap do repositório

**Objetivo:** esqueleto pronto para a Fase 2 começar sem atrito, em paralelo à Fase 1 (PoC). Não depende do aparelho.

## Escopo

- Árvore do TSD §15.2, `go.mod`, `Makefile`, `.gitignore`.
- Interfaces de fronteira (RNF-13), só os contratos:
  - `runit.ServiceManager` → `internal/runit/runit.go`
  - `proxy.ProxyClient` → `internal/proxy/proxy.go`
  - `system.DeviceProbe`, `Clock`, `CommandRunner`, `GitRunner` → `internal/system/system.go`
- `scripts/poc/measure.sh` (TSD A.8).
- Fixtures em `test/fixtures/`: `node`, `node-build`, `python`, `go`, `static`, `broken` (`BROKEN_MODE=crash|noport|loop`).
- README geral e um README por fase.

## Checklist

- ☑ Estrutura de diretórios e `go.mod`
- ☑ Interfaces de fronteira
- ☑ Fixtures e `measure.sh`
- ☑ READMEs
- ☑ `make test` e `make build` verdes no PC (2026-10-09; sem arquivos de teste ainda, só compilação)

## Decisões e desvios do TSD

- Interfaces ficam em seus pacotes de domínio; `Clock`, `CommandRunner` e `GitRunner` ficam em `internal/system` (o TSD não define pacote para elas).
- **Fakes e implementações reais não foram escritos na Fase 0**: cada um nasce com a sub-fase que o usa (2C runit, 2D proxy, 2E runner/git, 3B device). Evita código sem consumidor.
- `test/fixtures/go` é módulo Go próprio, fora do `./...` da raiz.
