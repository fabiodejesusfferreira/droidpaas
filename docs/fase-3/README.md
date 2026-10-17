# Fase 3 — Painel web

**Objetivo:** UI completa sobre a API da Fase 2 (SSR com `html/template` + htmx vendorizado, sem CDN). Detalhes: TSD §12 e §16.3.

## Sub-fases

- ☑ 3A Infra do painel, sessão, CSRF, cabeçalhos → AT-20, AT-21 (testes de handler)
- ☑ 3B Dashboard + `DeviceProbe` (Termux:API, `/proc`, `statfs`) → AT-27 (parsers testados; leitura real a validar no aparelho)
- ☑ 3C Assistente e página do app → AT-22, AT-24, AT-26, AT-30
- ☑ 3D Logs ao vivo (SSE) → AT-23, AT-25, AT-28
- ☑ 3E Configurações globais (chaves, tokens, túnel, conta)
- ☑ 3F Eventos e RSS/CPU por app
- ☑ 3G Endurecimento: CSP estrita, contraste WCAG AA (teste), 360 px sem rolagem horizontal (verificado no navegador em PC)

## Estado do gate

| Item | Estado |
|---|---|
| Testes de handlers e de segurança (CSRF, auth, rate limit, escape de log, i18n, contraste) | ☑ `go vet` + `go test ./...` verdes |
| Nenhum recurso externo (CSP `default-src 'self'`, sem inline) | ☑ por teste e inspeção no navegador |
| Layout 360 px e tema claro/escuro (PC, emulação) | ☑ |
| AT-20…AT-30 **no M22** e RNF-04 (dashboard p95 ≤ 300 ms na LAN) | ☐ roteiro pronto em `test/e2e/3-panel.sh`; **não executado** — aparelho inacessível na rede no momento (192.168.1.4 sem rota) |
| Validação do golden do Caddy (`trusted_proxies`) com Caddy real | ☐ `go test -tags termux -run Golden ./internal/proxy` no aparelho |
| Regressão das suítes `2d…2i` com o `droidd` novo | ☐ no aparelho |
| TSD v0.4 | ☑ |

Para fechar o gate, com o aparelho online: sincronize o código, rode `make e2e` (inclui `3-panel.sh`) e anexe a saída aqui.

## Decisões

- **SSR + htmx 2.0.4 vendorizado** (`web/static`, `go:embed`); JS próprio de ~3 KB (tema, confirmações, visualizador SSE); `allowEval=false`, `selfRequestsOnly`.
- **Mux único** no `droidd` (API + painel). `/api/v1` segue só com token; o painel usa sessão.
- **Sessão:** cookie `dp_session` (HttpOnly, SameSite=Lax, Secure quando HTTPS/proxy confiável); a tabela `sessions` guarda o SHA-256 do token; 12 h ociosa / 7 d absoluta; purga horária.
- **CSRF:** token por sessão (`X-CSRF-Token` ou `_csrf`) + checagem de Origin/Referer; no login, double-submit (`dp_login_csrf`).
- **Rate limit de login:** 5 falhas/5 min por IP+usuário, 20 por IP, atraso progressivo.
- **Logs:** ANSI e caracteres de controle removidos no servidor; `html/template` escapa; o cliente usa `textContent`.
- **Caddy:** `trusted_proxies` = loopback, para que o painel veja `X-Forwarded-Proto` do túnel e emita cookie `Secure`.
- **Dispositivo:** `termux-battery-status` com timeout de 5 s; falha → card "indisponível", nunca erro de página.
- **CLI:** novo `droid settings [set K=V…]` (substitui o `curl` para `base_domain`).
- **Desvios do TSD §12:** `/system` devolve também o JSON do dispositivo; `GET /api/v1/apps/{name}/stats`; sem WebSocket (SSE basta).
