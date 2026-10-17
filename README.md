# DroidPaaS

Micro-PaaS auto-hospedado que roda **nativamente no Termux** (Android, sem root, sem Docker): deploy por `git push`, supervisão via runit, proxy Caddy, túnel Cloudflare e painel web leve. Dispositivo de referência: Samsung Galaxy M22 (4 GB de RAM).

> A Fase 1 (PoC) validou as premissas centrais no aparelho (ver [docs/fase-1/poc-report.md](docs/fase-1/poc-report.md)); os números de desempenho do TSD seguem sendo metas até a Fase 2 medi-los.

## Status

| Fase | Descrição | Status | Docs |
|---|---|---|---|
| 0 | Bootstrap do repositório | ☑ concluída (`make test` e `make build` verdes) | [docs/fase-0](docs/fase-0/README.md) |
| 1 | Prova de Conceito no aparelho (go/no-go) | ☑ **GO condicional** (falta token/wildcard/SSE da Cloudflare; exige domínio, bloqueia só a sub-fase 2I) | [docs/fase-1](docs/fase-1/README.md) |
| 2 | MVP sem painel (`droidd` + `droid` + API) | ☑ **gate fechado** (soak de 72 h: 0 mortes, 17 060/17 060 sondas ok; reboot ok). Pendente, não bloqueante: túnel com domínio (RF-16) | [docs/fase-2](docs/fase-2/README.md) |
| 3 | Painel web | ☑ **concluída no aparelho** (e2e do painel 44/44; dashboard p95 42 ms; regressão 2d–2i verde). Pendente, não bloqueante: túnel com domínio e conferência visual no celular | [docs/fase-3](docs/fase-3/README.md) |

Especificação completa: [docs/tsd.md](docs/tsd.md).

## Estrutura

```
cmd/droidd, cmd/droid   binários (daemon e CLI)
internal/               pacotes (cada dependência do ambiente atrás de uma interface)
web/                    templates e assets do painel 
migrations/             SQL versionado
scripts/poc/            scripts da Fase 1 (measure.sh)
test/fixtures/          apps de exemplo (node, python, go, static, broken)
test/e2e/               roteiros para rodar no aparelho
docs/                   TSD e um README por fase
```

## Uso rápido (no Termux)

```bash
sh scripts/install.sh                       # compila no aparelho e roda `droidd init`
droid login --url http://127.0.0.1:8090 --token dp_...
droid apps create minha-api && droid keys add ~/.ssh/id_ed25519.pub
git push droid main                         # build e deploy aparecem no terminal
# painel: http://127.0.0.1:8090 (login do admin criado no `droidd init`)
```

Guia completo: [docs/operacao.md](docs/operacao.md).

## Desenvolvimento

Requer Go ≥ 1.22.

```bash
make test    # go test ./... (PC; usa runit/proxy/dispositivo falsos)
make build   # binários em bin/
make e2e     # no Termux: suítes de test/e2e com runit, Caddy, sshd e git reais num sandbox
```
