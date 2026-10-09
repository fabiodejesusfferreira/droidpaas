# DroidPaaS

Micro-PaaS auto-hospedado que roda **nativamente no Termux** (Android, sem root, sem Docker): deploy por `git push`, supervisão via runit, proxy Caddy, túnel Cloudflare e painel web leve. Dispositivo de referência: Samsung Galaxy M22 (4 GB de RAM).

> Projeto em fase de planejamento/PoC. Nada aqui foi validado no aparelho ainda; os números do TSD são metas, não medições.

## Status

| Fase | Descrição | Status | Docs |
|---|---|---|---|
| 0 | Bootstrap do repositório | ☑ esqueleto criado (build não verificado: Go não instalado no PC) | [docs/fase-0](docs/fase-0/README.md) |
| 1 | Prova de Conceito no aparelho (go/no-go) | ☐ | [docs/fase-1](docs/fase-1/README.md) |
| 2 | MVP sem painel (`droidd` + `droid` + API) | ☐ | [docs/fase-2](docs/fase-2/README.md) |
| 3 | Painel web | ☐ | [docs/fase-3](docs/fase-3/README.md) |

Especificação completa: [docs/tsd.md](docs/tsd.md).

## Estrutura

```
cmd/droidd, cmd/droid   binários (daemon e CLI)
internal/               pacotes (cada dependência do ambiente atrás de uma interface)
web/                    templates e assets do painel (Fase 3)
migrations/             SQL versionado
scripts/poc/            scripts da Fase 1 (measure.sh)
test/fixtures/          apps de exemplo (node, python, go, static, broken)
test/e2e/               roteiros para rodar no aparelho
docs/                   TSD e um README por fase
```

## Desenvolvimento

Requer Go ≥ 1.22.

```bash
make test    # go test ./...
make build   # binários em bin/
```
