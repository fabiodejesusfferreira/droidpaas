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
| Painel no M22 (`test/e2e/3-panel.sh`, sandbox com runit/Caddy/git reais): login/logout/sessão, CSRF (sem token, token errado, Origin externo), limite de login (429), cookie `Secure` atrás do Caddy, dashboard com bateria/RAM/disco reais, deploy pelo painel com log, env com segredo mascarado + reveal + reinício, logs de runtime por SSE | ☑ 44 ok, 0 falhas |
| RNF-04 — dashboard no M22 (30 requisições, LAN, loopback do aparelho) | ☑ mediana 36 ms · **p95 42 ms** (meta ≤ 300 ms); página do app p95 28 ms |
| Golden do Caddy (`trusted_proxies`) com Caddy real | ☑ `TestGoldenValidatesWithRealCaddy` passa no aparelho |
| Regressão `2d…2i` com o `droidd` novo | ☑ 9 + 22 + 29 + 24 + 44 + 26 ok, 0 falhas (`make e2e` agora inclui `3-panel`) |
| Instalação real atualizada (`droidd` reiniciado; Caddy e apps seguiram no ar) | ☑ `/login` 200, `/` 303, `droid settings` ok |
| Verificação visual em desktop e em 360 px | ◐ feita no navegador do PC com dados de demonstração (sem rolagem horizontal, temas, htmx sob CSP); **não** repetida abrindo o painel real pelo navegador do celular |

**Túnel público com domínio (RF-16, V-05 Parte B) — validado em 2026-10-17** com `dejesusdevts.com`: túnel nomeado `connected` no M22; rotas `painel.` e `ola.` (hostname a hostname, porque a raiz já serve um portfólio); `ola.dejesusdevts.com` responde 200 em ~0,3 s pela internet; `painel.` atrás de Cloudflare Access (302 para o login sem sessão; após login por código o painel abre com bateria, RAM e disco reais em navegador desktop). Cookie `Secure` atrás do túnel confirmado (`dp_login_csrf` com `Secure`). Primeiro deploy real (`git push` por SSH a partir do Termux): `succeeded` em 8 s. Logs de runtime ao vivo (SSE) pelo túnel nomeado + Access confirmados pelo usuário no navegador desktop: a linha `GET /teste-ao-vivo` apareceu em até 2 s (AT-25). Deploy #2 (rebuild pelo botão) concluído e exibido no painel.

Conferidos à mão pelo usuário no aparelho real (fora do `3-panel.sh`): AT-26 (rollback pelo botão) e AT-30 (remoção de um app descartável) funcionaram; o assistente em 3 passos criou o app e a página de publicação orientou o `git push`. Túnel/SSE pelo túnel nomeado segue dependendo do domínio na Cloudflare.


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
