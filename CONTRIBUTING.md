# Como contribuir

Obrigado pelo interesse! Este projeto nasceu para ser simples de entender e de rodar num celular. Contribuições pequenas e focadas são as melhores.

## Antes de começar

- Procure uma issue existente ou abra uma descrevendo o problema ou a ideia. Para mudanças grandes, converse antes de escrever o código.
- Leia [docs/tsd.md](docs/tsd.md) (especificação) e [docs/operacao.md](docs/operacao.md) (como o sistema funciona na prática).

## Rodando no seu computador

Requer Go 1.26 ou mais novo.

```bash
make test    # testes (usam runit, proxy e dispositivo falsos)
make build   # binários em bin/
```

Para ver o painel com dados fictícios: `go test -tags demo -run TestDemo -timeout 0 ./internal/web` e abra `http://127.0.0.1:18999` (usuário `admin`, senha `senha-de-teste-123`).

Testes que dependem do aparelho (runit, Caddy, sshd e git reais) ficam em `test/e2e/` e rodam no Termux com `make e2e`. Se a sua mudança toca nessas partes, rode essas suítes se puder e diga no PR o que foi (ou não foi) testado.

## O que esperamos de um PR

- `gofmt`, `go vet ./...` e `go test ./...` passando (o CI confere).
- Mudança pequena e com um teste quando houver lógica nova (um ramo, um parser, segurança).
- Textos do painel e das mensagens em **português do Brasil**, no catálogo `internal/web/i18n.go` (existe um teste que confere as chaves).
- Nada de dependências externas carregadas no painel (a CSP bloqueia scripts e estilos de fora) e nenhum script ou estilo inline.
- Sem segredos, tokens, IPs ou domínios pessoais em código, testes e docs.

## Mensagens de commit

Em português, no formato `tipo(escopo): resumo`, por exemplo `fix(web): quebra textos longos nos avisos`.

## Segurança

Para vulnerabilidades, siga o [SECURITY.md](SECURITY.md) e **não** abra uma issue pública.
