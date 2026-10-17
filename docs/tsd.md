# DroidPaaS — Technical Specification Document (TSD)

| Campo | Valor |
|---|---|
| **Projeto** | DroidPaaS (nome provisório) |
| **Versão do documento** | 0.4 — Fase 3 implementada (aceitação no aparelho pendente) |
| **Data** | 2026-10-13 (v0.1: 2026-10-09) |
| **Escopo deste documento** | Fases 1, 2 e 3 (Prova de Conceito, MVP sem painel, Painel web) |
| **Dispositivo-alvo de referência** | Samsung Galaxy M22, 4 GB de RAM (aparelho de testes/sandbox) |
| **Plataforma** | Android + Termux (sem root) |
| **Status das premissas** | V-01…V-15 avaliadas na Fase 1 (resultados em §2.5 e em `docs/fase-1/poc-report.md`). Pendente: V-05 Parte B (token + wildcard + SSE pelo túnel nomeado), que exige um domínio na Cloudflare. |

> **Aviso de honestidade técnica:** este documento descreve um design, não um sistema já testado. Nomes de pacotes, comportamentos do Termux/Android e números de desempenho são **hipóteses** até a Fase 1 comprová-las. Os limites numéricos (RAM, tempo, temperatura) são **metas propostas**, não medições.

### Histórico de versões

| Versão | Data | Mudanças |
|---|---|---|
| 0.1 | 2026-10-09 | Rascunho inicial |
| 0.4 | 2026-10-16 | Fase 3 implementada (§12): mux único (API + painel); sessões na tabela `sessions` (SHA-256 do token; 12 h ociosa/7 d); CSRF por sessão + Origin; `trusted_proxies` (loopback) no Caddy para cookie `Secure` atrás do túnel; `/system` com JSON do dispositivo; `GET /apps/{name}/stats`; sem WebSocket (SSE); CLI `droid settings`. AT-20…30 no M22 e RNF-04 pendentes de execução do `test/e2e/3-panel.sh` |
| 0.3 | 2026-10-13 | Fase 2 implementada (§16.2.1): `chpst` não executa scripts com shebang (A.1 usa `sh start.sh`); codificação do envdir; migrações 0002–0004; endpoints `/releases`, `/backup`, `/system`, Git HTTP; `git-shell`/`hook` com `--home`/`--socket`; Q-01, Q-04, Q-05, Q-07, Q-08, Q-10 decididas |
| 0.2 | 2026-10-12 | Resultados da Fase 1 (§2.5); portas padrão do proxy `18080`/Admin `12019`; `nice` best-effort; `/proc/stat` removido das métricas; boot em arquivo próprio; token do túnel via `TUNNEL_TOKEN`/`--token-file`; Q-02, Q-03 e Q-09 decididas/parciais |

---

## Sumário

1. [Introdução](#1-introdução)
2. [Contexto, restrições e premissas](#2-contexto-restrições-e-premissas)
3. [Requisitos](#3-requisitos)
4. [Arquitetura](#4-arquitetura)
5. [Decisões arquiteturais (ADRs)](#5-decisões-arquiteturais-adrs)
6. [Layout em disco e configuração](#6-layout-em-disco-e-configuração)
7. [Modelo de dados](#7-modelo-de-dados)
8. [Especificação dos componentes](#8-especificação-dos-componentes)
9. [Fluxos detalhados](#9-fluxos-detalhados)
10. [API REST](#10-api-rest)
11. [CLI `droid`](#11-cli-droid)
12. [Painel web (Fase 3)](#12-painel-web-fase-3)
13. [Segurança](#13-segurança)
14. [Observabilidade e operação](#14-observabilidade-e-operação)
15. [Estrutura do repositório e stack](#15-estrutura-do-repositório-e-stack)
16. [Plano por fases](#16-plano-por-fases)
17. [Estratégia de testes](#17-estratégia-de-testes)
18. [Riscos e mitigações](#18-riscos-e-mitigações)
19. [Fora de escopo e evolução futura](#19-fora-de-escopo-e-evolução-futura)
20. [Questões em aberto](#20-questões-em-aberto)
- [Apêndice A — Templates e scripts](#apêndice-a--templates-e-scripts)

---

## 1. Introdução

### 1.1 Propósito

Especificar tecnicamente o **DroidPaaS**, um micro-PaaS auto-hospedado que roda **nativamente no Termux**, sem Docker, e permite publicar APIs e sites a partir de um celular Android com uma experiência semelhante (em conceito) à do Dokploy: criar apps, fazer deploy via Git, configurar variáveis, domínios, ver logs e gerenciar tudo por um painel web.

### 1.2 Problema

Docker e ferramentas derivadas (Compose, Dokploy) não rodam de forma útil em celulares como o Galaxy M22 (kernel do fabricante sem recursos de container para apps comuns, sem root, 4 GB de RAM, sem virtualização aproveitável). O usuário já hospeda vários apps leves no celular e precisa de uma **camada de gerenciamento** que substitua scripts soltos por algo decente.

### 1.3 Objetivos

- **O1.** Deploy por `git push` (SSH e, opcionalmente, HTTP) com build automático.
- **O2.** Supervisão de processos confiável, com reinício automático e detecção de crash loop.
- **O3.** Roteamento por hostname (proxy reverso) e exposição pública via túnel (CGNAT-friendly).
- **O4.** Painel web leve, mobile-first, com logs ao vivo.
- **O5.** Baixo consumo: o próprio DroidPaaS deve ocupar uma fração pequena dos ~1,5 GB úteis do aparelho.
- **O6.** Funcionar **sem root**.

### 1.4 Escopo deste documento

| Fase | Nome | Resultado |
|---|---|---|
| **1** | Prova de Conceito | Ambiente manual validado + medições de 24 h + decisão go/no-go |
| **2** | MVP sem painel | `droidd` + `droid` (CLI) + API: deploy por git, build, runit, Caddy, túnel, env vars, logs |
| **3** | Painel web | UI completa sobre a API da Fase 2 |

### 1.5 Fora de escopo (nesta versão)

- **Governador térmico/bateria ativo** (política de pausar builds/parar apps) → Fase 4. Na Fase 3 há apenas **leitura/exibição** do estado do dispositivo.
- Escala para zero (parar apps ociosos e acordar sob demanda) → Fase 4.
- Bancos de dados gerenciados (Postgres/Redis), webhooks do GitHub, backend Docker/VM, runtime WASM → Fase 5+.
- Isolamento entre apps (sem namespaces/cgroups) e multi-usuário/multi-tenant.
- Multi-nó / múltiplos celulares.

### 1.6 Convenções

As palavras **DEVE**, **NÃO DEVE**, **DEVERIA** e **PODE** seguem o sentido da RFC 2119. Prioridades: **M** (Must), **S** (Should), **C** (Could).

### 1.7 Glossário

| Termo | Significado |
|---|---|
| **App** | Unidade implantável (processo web ou site estático) |
| **Release** | Snapshot de código + dependências construído a partir de um commit |
| **Deploy** | Execução do pipeline que produz e ativa uma release |
| **runit / runsv** | Supervisor de processos usado pelo `termux-services` |
| **envdir** | Diretório em que cada arquivo é uma variável de ambiente (usado com `chpst -e`) |
| **Estado desejado** | O que o usuário quer (ex.: app "rodando") gravado no SQLite |
| **Estado observado** | O que de fato está acontecendo (runit, Caddy) |
| **Reconciliador** | Laço que faz o observado convergir ao desejado |
| **Termux:API / Termux:Boot** | Apps complementares do Termux (sensores/notificações; execução no boot) |

---

## 2. Contexto, restrições e premissas

### 2.1 Dispositivo de referência

| Item | Valor de referência | Observação |
|---|---|---|
| Modelo | Samsung Galaxy M22 | Aparelho de testes |
| RAM | 4 GB | Estima-se ~1,5 GB úteis para o usuário (a confirmar na Fase 1) |
| SoC | MediaTek, ARM64 | Confirmar modelo e versão do Android no aparelho |
| Root | Não | Fora de questão |
| Virtualização | Não utilizável | Docker/VM descartados |

### 2.2 Restrições da plataforma (Android + Termux)

| ID | Restrição | Impacto no design |
|---|---|---|
| C-01 | Sem root | Sem cgroups/namespaces; controle de recursos só cooperativo |
| C-02 | Apps não abrem portas < 1024 | Proxy em porta alta (ex.: 18080; a 8080 costuma estar ocupada por outros apps, ver §2.5); o túnel cuida do 80/443 público |
| C-03 | Sem systemd | Supervisão via runit (`termux-services`) |
| C-04 | Libc é Bionic (não glibc) | Binários nativos de npm/pip podem não existir ou não compilar |
| C-05 | "Phantom process killer" (Android 12+) e gestão de bateria do fabricante | Exige configuração via adb e isenções; risco de processos mortos |
| C-06 | Todos os processos rodam sob o mesmo UID do Termux | **Sem isolamento entre apps**; só hospedar código confiável |
| C-07 | Sem `/etc/resolv.conf` convencional | Binários Go estáticos "puros" podem falhar em DNS; preferir build no Termux |
| C-08 | Armazenamento interno com limites; `/sdcard` não serve para executáveis/sockets | Tudo sob `$HOME` do Termux |
| C-09 | Calor e bateria em uso contínuo | Requisito de observabilidade já na Fase 3; política na Fase 4 |

### 2.3 Premissas

- **P-01.** Termux instalado pelo **F-Droid ou GitHub** (não Play Store), junto com Termux:API e Termux:Boot.
- **P-02.** O aparelho fica na tomada, com acesso à internet (Wi-Fi), em local ventilado.
- **P-03.** O usuário é o único administrador; todo código hospedado é dele (confiável).
- **P-04.** O domínio de produção usa Cloudflare (DNS + Tunnel). Tailscale é opcional para acesso privado ao painel/SSH.
- **P-05.** O `droidd` é compilado no próprio aparelho (`go build` no Termux) ou em CI que gere binário compatível com Termux.

### 2.4 Itens a validar na Fase 1

Cada item abaixo é uma **hipótese**. O resultado deve ser registrado no relatório da Fase 1 (§16.1).

| ID | Hipótese a validar | Impacto se falsa |
|---|---|---|
| V-01 | `termux-battery-status` retorna temperatura (°C) e % e atualiza com a tela apagada | Sem sensor, o widget de status do dispositivo (RF-39) é descartado ou degradado |
| V-02 | `termux-services` sobe o `runsvdir` no boot via Termux:Boot e detecta novos serviços em poucos segundos | Trocar por `runsvdir` próprio ou supervisor em Go |
| V-03 | `modernc.org/sqlite` compila e roda no Termux (Go no Android/arm64) | Fallback: driver CGO (`mattn/go-sqlite3`) com clang do Termux |
| V-04 | Pacote `caddy` disponível e aceita configuração JSON via Admin API com HTTPS automático desativado | Alternativa: nginx com reload por arquivo |
| V-05 | `cloudflared` disponível; modo **token** funciona; hostname **wildcard** é aceito no dashboard (com o CNAME wildcard criado) | Gerenciar `config.yml` local com um hostname por app |
| V-06 | `sshd` do Termux respeita `command=` forçado em `authorized_keys` e `git-receive-pack` funciona via SSH | Usar apenas Git HTTP |
| V-07 | O pacote `git` do Termux inclui `git http-backend` | Implementar Git HTTP de outra forma ou descartar RF-05 |
| V-08 | O phantom process killer pode ser desativado via adb no M22 (versão do Android a confirmar) e os processos sobrevivem 24 h com tela apagada | Revisar toda a estratégia de execução contínua |
| V-09 | Pacote `runit` fornece `sv`, `chpst`, `svlogd`; `chpst -n` (nice) funciona | Reimplementar partes no supervisor próprio |
| V-10 | Leitura de `/proc/<pid>/status` e `/proc/stat` é permitida para processos do mesmo UID | Métricas por app ficam indisponíveis (RF-42 vira "Could") |
| V-11 | Builds típicos (um app Node e um Python) completam no M22 sem ser mortos por falta de memória | Reduzir escopo de runtimes ou exigir build externo |
| V-12 | Socket Unix em `$HOME` funciona entre `droid` (hook/CLI) e `droidd` | Usar TCP em `127.0.0.1` com token |
| V-13 | Binário Go compilado no Termux resolve DNS corretamente | Configurar resolvedor explícito |
| V-14 | O pacote `golang` do Termux é suficientemente recente (Go ≥ 1.22 para o `ServeMux` com padrões) | Usar roteador de terceiros leve |
| V-15 | Termux:Boot executa no boot do aparelho sem bloqueio do One UI | Documentar isenções/alternativas |

### 2.5 Resultados da Fase 1 (PoC no Galaxy M22, Android 13)

Evidências e comandos em `docs/fase-1/poc-report.md`. Soak do PoC: **67,8 h**, 0 reinícios, 0 falhas de sonda, temperatura máx. 41,7 °C, RAM disponível mín. 415 MB (boot com apps do usuário).

| ID | Resultado | Observação |
|---|---|---|
| V-01 | Confirmada | `termux-battery-status` funciona com tela apagada |
| V-02 | Confirmada | `runsvdir` detecta serviço novo em ≤ 3 s; `sv`, `kill -9` → reinício em ≤ 1 s; serviços voltam no boot |
| V-03 | Confirmada | `modernc.org/sqlite` v1.60.1 compila em ~105 s no aparelho; WAL e `VACUUM INTO` OK |
| V-04 | Confirmada | Caddy sobe como serviço, `POST /load` OK; após reiniciar o Caddy a config se perde (o reconciliador deve reaplicar) |
| V-05 | **Parcial** | `cloudflared` roda sob runit e o quick tunnel funciona de fora; **token + wildcard + SSE pendentes** (sem domínio). Quick tunnels mudam de URL a cada restart e não suportam SSE |
| V-06 | Confirmada | `command=` forçado, restrição de app/traversal, `git push` do PC com saída `remote:` ao vivo |
| V-07 | Confirmada | `git-http-backend` presente e funcional (CGI) |
| V-08 | Confirmada | Phantom killer desativado via adb; 67,8 h sem mortes |
| V-09 | **Parcial** | `sv`, `chpst -e`, `svlogd` OK; **`chpst -n`/`nice` sem efeito** no Android 13 |
| V-10 | **Parcial** | `/proc/<pid>/status` e `/stat` OK; `/proc/stat` e `/proc/loadavg` negados |
| V-11 | Confirmada (apps puros) | Node e Python em ~36 s e ~30 s; dependência nativa sem wheel (numpy) inviável |
| V-12 | Confirmada | Socket Unix `0600`, alcançável de dentro do hook sob o `sshd` |
| V-13 | Confirmada | Binário Go compilado no aparelho resolve DNS e fala HTTPS; o alvo `GOOS=linux` do Makefile **não** foi validado |
| V-14 | Confirmada | Go 1.27.1 no Termux |
| V-15 | Confirmada | Termux:Boot executa; coexiste com o `start-services` do usuário |

**Mudanças de design decorrentes:**

1. **Portas configuráveis e padrão fora das comuns:** Caddy `127.0.0.1:18080`, Admin `127.0.0.1:12019` (a 8080/2019 conflitam com outros apps do aparelho). O `droidd init` detecta portas ocupadas.
2. **`nice` é best-effort** (campo e `chpst -n` mantidos, sem garantia).
3. **Métricas:** CPU/RSS por app via `/proc/<pid>`; não há CPU global nem load average.
4. **Boot:** arquivo próprio em `~/.termux/boot/`; nunca sobrescrever o script do usuário.
5. **Reconciliador reaplica a config do Caddy** quando o `dp-caddy` reinicia (já previsto em §8.5.3, agora comprovado).
6. **Builds no aparelho** (P-05 validada); Python limitado a pacotes puros ou com wheel.
7. **Túnel:** token por `TUNNEL_TOKEN` (envdir) ou `--token-file`; **requer domínio** na Cloudflare antes da sub-fase 2I.

---

## 3. Requisitos

### 3.1 Requisitos funcionais

#### Fase 2 — MVP (core, sem painel)

| ID | Requisito | Prio. |
|---|---|---|
| RF-01 | Criar app (nome, branch de deploy, runtime automático ou manual) | M |
| RF-02 | Listar, detalhar, editar e remover apps | M |
| RF-03 | Iniciar, parar e reiniciar app | M |
| RF-04 | Deploy por `git push` via SSH com saída de build no terminal do `git push` | M |
| RF-05 | Deploy por `git push` via HTTP (Git smart HTTP) com token | S |
| RF-06 | Deploy manual (re-deploy/rebuild de uma ref ou da última release) | M |
| RF-07 | Detectar runtime: `node`, `python`, `go`, `static`, `custom` (via `Procfile`/`droidpaas.toml`) | M |
| RF-08 | Fila de builds com concorrência configurável (padrão 1) | M |
| RF-09 | Health check pós-deploy e **rollback automático** se falhar | M |
| RF-10 | Rollback manual para uma release anterior bem-sucedida | M |
| RF-11 | Retenção e coleta de releases antigas (padrão: 5 por app) | M |
| RF-12 | CRUD de variáveis de ambiente por app, injetadas via envdir | M |
| RF-13 | Roteamento por hostname: `<app>.<domínio_base>` automático | M |
| RF-14 | Domínios adicionais por app | S |
| RF-15 | Servir sites estáticos diretamente pelo proxy | M |
| RF-16 | Gerenciar o `cloudflared` (modo token) como serviço supervisionado | S |
| RF-17 | Logs de runtime: últimas N linhas e *follow* | M |
| RF-18 | Logs de build persistidos por deploy | M |
| RF-19 | Reconciliador: estado desejado → observado (runit e proxy) | M |
| RF-20 | Detecção de *crash loop* com marcação de estado `crashed` e parada do reinício | S |
| RF-21 | Registro de eventos/auditoria | S |
| RF-22 | API REST com autenticação por token | M |
| RF-23 | CLI `droid` cobrindo as operações principais | M |
| RF-24 | Comando de inicialização (`droidd init`): cria diretórios, banco, usuário admin e primeiro token | M |
| RF-25 | Backup/export do banco (`droid backup`) | C |

#### Fase 3 — Painel

| ID | Requisito | Prio. |
|---|---|---|
| RF-30 | Login com sessão (cookie) e logout | M |
| RF-31 | Dashboard com lista de apps, status e acesso rápido | M |
| RF-32 | Assistente de criação de app | M |
| RF-33 | Página do app com abas: Visão geral, Deploys, Ambiente, Logs, Domínios, Configurações | M |
| RF-34 | Logs ao vivo (SSE) de deploy e de runtime | M |
| RF-35 | Ações: Deploy/Rebuild/Rollback e Start/Stop/Restart | M |
| RF-36 | Gestão de chaves SSH e tokens de API | M |
| RF-37 | Configurações globais: domínio base, túnel, retenção, Git HTTP | M |
| RF-38 | Página de eventos | S |
| RF-39 | Widget de **status do dispositivo** (bateria, temperatura, RAM, disco) — **somente leitura** | S |
| RF-40 | UI responsiva (mobile-first) com tema claro/escuro | S |
| RF-41 | Troca de senha do administrador | M |
| RF-42 | Exibir RSS/CPU por app | S |

### 3.2 Requisitos não funcionais

> Todos os números são **metas propostas**, a serem calibradas na Fase 1.

| ID | Categoria | Requisito | Meta |
|---|---|---|---|
| RNF-01 | Footprint | RSS do `droidd` ocioso | ≤ 40 MB |
| RNF-02 | Footprint | CPU do `droidd` ocioso (média) | < 1 % |
| RNF-03 | Footprint | Banco SQLite e logs com rotação | Crescimento limitado e configurável |
| RNF-04 | Desempenho | Render de página do painel na rede local (p95) | ≤ 300 ms |
| RNF-05 | Desempenho | Deploy de app "hello-world" Node (pós-clone) | ≤ 90 s |
| RNF-06 | Resiliência | Reinício automático de app que cai | ≤ 5 s |
| RNF-07 | Resiliência | Retomada após reboot (a partir do Termux:Boot) | ≤ 60 s |
| RNF-08 | Resiliência | Queda do `droidd` não derruba apps nem proxy | Obrigatório |
| RNF-09 | Resiliência | Deploy que falha **antes** da troca não afeta a release em execução | Obrigatório |
| RNF-10 | Confiabilidade | Soak test sem morte não intencional de processos | 72 h |
| RNF-11 | Segurança | Painel e API nunca expostos sem autenticação | Obrigatório |
| RNF-12 | Portabilidade | Operar sem root em Termux com Android 11+ (a confirmar) | Obrigatório |
| RNF-13 | Manutenibilidade | Código testável em PC via interfaces para tudo que depende do aparelho | Obrigatório |
| RNF-14 | Usabilidade | Painel utilizável em tela de celular (≥ 360 px) | Obrigatório |
| RNF-15 | Atualização | Atualização do `droidd` sem perder apps (migrações de banco versionadas) | Obrigatório |

---

## 4. Arquitetura

### 4.1 Visão geral

```
                          Internet
                             │
              Cloudflare (DNS + Tunnel)          Tailscale (opcional,
                             │                    acesso privado)
                 ┌───────────▼───────────┐              │
                 │ cloudflared (runit)   │              │
                 └───────────┬───────────┘              │
                             │ http://127.0.0.1:18080    │
                 ┌───────────▼───────────┐              │
                 │ Caddy (runit)         │              │
                 │ roteia por Host       │◄─────────────┼──── Admin API 127.0.0.1:12019
                 └──┬──────────┬─────────┘              │              ▲
                    │          │ file_server (estáticos)│              │
        127.0.0.1:2xxxx        └────────────────┐       │              │
        (processos dos apps,                    │       │              │
         1 serviço runit por app)               │       │              │
                                                │       │              │
   ┌────────────────────────────────────────────┴───────▼──────────────┴───┐
   │ droidd (runit)                                                         │
   │  API+Painel 127.0.0.1:8090 · Unix socket · Builder · Reconciliador     │
   │  Store (SQLite) · Runit ctl · Proxy ctl · Tunnel ctl · Git/SSH mgr     │
   └─────────────▲──────────────────────────────────────────────────────────┘
                 │ unix socket
   sshd :8022 ──► git-shell forçado ──► post-receive hook ──► `droid hook`
```

### 4.2 Processos no dispositivo

| Processo | Gerenciado por | Papel |
|---|---|---|
| `droidd` | runit (instalado por `droidd init`) | Plano de controle: API, painel, builds, reconciliação |
| `caddy` | runit (`dp-caddy`) | Proxy reverso e servidor de estáticos |
| `cloudflared` | runit (`dp-cloudflared`), criado pelo `droidd` quando há token | Túnel de saída para a Cloudflare |
| `sshd` | `termux-services` | Entrada de `git push` via SSH |
| Apps | runit (`dp-app-<nome>`) | Um processo (ou árvore) por app |

**Princípio central:** o plano de dados (apps, Caddy, túnel) é supervisionado pelo runit e **continua funcionando se o `droidd` cair** (RNF-08).

### 4.3 Fluxo de uma requisição pública

1. Cliente → DNS da Cloudflare → túnel → `cloudflared` local.
2. `cloudflared` encaminha para `http://127.0.0.1:18080` (Caddy).
3. Caddy escolhe a rota pelo header `Host`: `reverse_proxy 127.0.0.1:<porta do app>` ou `file_server` (estático).
4. Sem rota → página 404 do DroidPaaS; app parado → página 503 do DroidPaaS.

### 4.4 Módulos internos do `droidd`

| Módulo | Responsabilidade |
|---|---|
| `config` | Carregar `droidd.toml`, validar, expandir `~`/`$PREFIX` |
| `store` | SQLite, migrações, repositórios (apps, releases, deploys, env, domínios, eventos, usuários) |
| `auth` | Hash de senha, sessões, tokens, rate limit de login |
| `apps` | Regras de negócio de apps (validação, portas, estado desejado) |
| `build` | Detectores, pipeline de build, fila, timeouts, logs de build |
| `deploy` | Orquestração: build → troca → start → health → rollback |
| `runit` | Interface `ServiceManager` (gera diretórios de serviço, `sv`, leitura de estado) |
| `proxy` | Gera a config JSON do Caddy a partir do estado desejado e aplica via Admin API |
| `tunnel` | Gerencia o serviço `dp-cloudflared` (modo token) |
| `gitsrv` | Repositórios bare, hooks, `authorized_keys` gerenciado, Git HTTP |
| `logs` | Leitura/follow de logs do svlogd, retenção de logs de build |
| `system` | Leitura de bateria/temperatura (Termux:API), RAM, disco, RSS/CPU por app |
| `reconcile` | Laço de convergência desejado → observado |
| `events` | Registro de eventos/auditoria |
| `api` | Handlers REST, SSE, socket Unix interno |
| `web` | Templates, assets embutidos e handlers do painel (Fase 3) |

---

## 5. Decisões arquiteturais (ADRs)

| ID | Decisão | Alternativas consideradas | Justificativa |
|---|---|---|---|
| ADR-01 | **Sem Docker**; apps como processos nativos do Termux | Docker em VM, proot+udocker | O aparelho de referência não suporta virtualização útil; overhead e calor inaceitáveis |
| ADR-02 | **Go**, binário único para `droidd` e `droid` | Node, Python, Rust | Baixo consumo de RAM, sem runtime extra, boa concorrência, fácil de embutir assets |
| ADR-03 | **runit via `termux-services`** (prefixo `dp-` nos serviços) | Supervisor próprio em Go; `runsvdir` dedicado; PM2 | Já é o init do Termux; sobrevive à queda do `droidd`. *Alternativa registrada:* `runsvdir` dedicado (revisitar se V-02 falhar) |
| ADR-04 | **Estado desejado no SQLite + reconciliador idempotente** | Comandos imperativos sem estado | Tolerância a falhas, retomada após reboot, correção automática de deriva |
| ADR-05 | **Caddy configurado por config completa** (`POST /load`) a cada mudança | Patch incremental de rotas | Idempotente e atômico; a config é pequena; o estado desejado vive no banco |
| ADR-06 | **Túnel com hostname wildcard** apontando para o Caddy (`*.dominio` → `127.0.0.1:18080`) | Um ingress por app em `config.yml` | O `droidd` não precisa tocar no `cloudflared` a cada app; só o Caddy muda. *Sujeito a V-05* |
| ADR-07 | **Variáveis via envdir + `chpst -e`** | Arquivo `.env` com `source` | Evita problemas de escaping/injeção em shell |
| ADR-08 | **htmx + `html/template` + assets embutidos** | SPA (React/Vue) | Sem build de frontend, pouca RAM, funciona offline e em rede lenta |
| ADR-09 | **SQLite em modo WAL** | Banco externo | Zero servidor, baixo consumo |
| ADR-10 | **Release em diretório próprio + symlink `current`** | Atualizar in-place | Rollback trivial (trocar symlink) e deploy atômico |
| ADR-11 | **Build antes de parar a release atual** | Parar → build → iniciar | Falha de build não causa downtime (RNF-09). Custo: pico de RAM/CPU durante o build |
| ADR-12 | **Painel e API em `127.0.0.1:8090` por padrão** | Bind em `0.0.0.0` | Exposição só por túnel autenticado/Tailscale, evitando abertura acidental na LAN |
| ADR-13 | **Socket Unix** para o hook do git e a CLI local | TCP local | Sem porta exposta; permissões de arquivo (sujeito a V-12) |

---

## 6. Layout em disco e configuração

### 6.1 Diretórios

`$DP_HOME` = `~/droidpaas` (configurável).

```
$DP_HOME/
├── bin/                      # droidd, droid (ou links)
├── etc/
│   ├── droidd.toml           # configuração
│   └── secrets/master.key    # chave de cifragem de segredos (0600)
├── data/
│   └── droidpaas.db          # SQLite (WAL)
├── run/
│   └── droidd.sock           # socket Unix interno (0600)
├── apps/
│   └── <app>/
│       ├── repo.git/         # repositório bare (hooks/post-receive)
│       ├── releases/
│       │   └── <timestamp>-<sha7>/   # código + dependências
│       ├── current -> releases/<...>
│       ├── env/              # envdir (um arquivo por variável, 0600)
│       ├── shared/           # dados persistentes (DP_DATA_DIR)
│       └── start.sh          # comando de start gerado (0700)
├── logs/
│   ├── apps/<app>/           # svlogd (current, @timestamps)
│   └── deploys/<app>/<id>.log
├── cache/                    # npm, pip, go build cache
├── proxy/
│   ├── bootstrap.json        # config inicial do Caddy
│   └── pages/                # 404.html, 503.html
└── tmp/
```

Diretórios de serviço (runit) ficam no `SVDIR` do `termux-services` (`$PREFIX/var/service`):

```
$PREFIX/var/service/
├── droidd/                   # criado por `droidd init`
├── dp-caddy/
├── dp-cloudflared/           # criado quando há token
└── dp-app-<nome>/
    ├── run
    ├── down                  # presente = serviço parado
    └── log/run               # svlogd -> $DP_HOME/logs/apps/<nome>
```

### 6.2 Exemplo de `droidd.toml`

```toml
[paths]
home = "~/droidpaas"

[http]
panel_addr  = "127.0.0.1:8090"
trusted_proxies = ["127.0.0.1"]     # para X-Forwarded-Proto/For
secure_cookies  = "auto"            # auto | always | never

[proxy]
listen = "127.0.0.1:18080"
admin  = "127.0.0.1:12019"
base_domain = ""                    # ex.: "apps.exemplo.com" (defina no init/painel)
panel_hostname = ""                 # opcional: rotear o painel por hostname

[runit]
service_dir = "$PREFIX/var/service"
prefix      = "dp-"

[ports]
range_start = 20000
range_end   = 29999

[build]
max_concurrent = 1
timeout        = "15m"
nice           = 10
keep_logs_days = 30

[deploy]
keep_releases  = 5
health_timeout = "30s"
health_interval = "1s"

[git]
ssh_enabled  = true
http_enabled = true
auto_create_apps = false            # criar app no primeiro push?
authorized_keys  = "~/.ssh/authorized_keys"

[reconcile]
interval = "10s"
crash_loop_window    = "60s"
crash_loop_threshold = 5

[logging]
level  = "info"
format = "json"
```

### 6.3 Arquivo opcional por app: `droidpaas.toml` (na raiz do repositório)

```toml
runtime     = "node"                # node | python | go | static | custom
build       = "npm run build"       # opcional
start       = "node server.js"      # opcional (senão Procfile/convenção)
health_path = "/health"             # opcional (senão checagem TCP)
health_timeout = "30s"
static_dir  = "dist"                # para runtime static
nice        = 10
```

Ordem de precedência da configuração do app: **campos editados no painel/API > `droidpaas.toml` > `Procfile` > convenções por runtime**.

---

## 7. Modelo de dados

Convenções: datas em **epoch milissegundos** (`INTEGER`); IDs `INTEGER PRIMARY KEY`; todo `app_id` com `ON DELETE CASCADE`. Pragmas: `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`.

### 7.1 DDL (versão inicial)

> Evolução por migrações (v0.3): `0002` adiciona `api_tokens.scope` e `apps.crashed_at` (o estado `crashed` precisa sobreviver a um restart); `0003` adiciona `releases.runtime`; `0004` adiciona `deploys.git_sha/git_ref/rebuild` (a fila precisa saber o que construir). O DDL abaixo é o da `0001`.

```sql
CREATE TABLE schema_migrations (
  version     INTEGER PRIMARY KEY,
  applied_at  INTEGER NOT NULL
);

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,                 -- argon2id (PHC string)
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
  id           TEXT PRIMARY KEY,               -- SHA-256 do token do cookie
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_secret  TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  user_agent   TEXT,
  ip           TEXT
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,           -- SHA-256 do token
  prefix       TEXT NOT NULL,                  -- primeiros chars, p/ identificar na UI
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at   INTEGER
);

CREATE TABLE ssh_keys (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  public_key  TEXT NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  created_at  INTEGER NOT NULL
);

CREATE TABLE apps (
  id                 INTEGER PRIMARY KEY,
  name               TEXT NOT NULL UNIQUE,     -- ^[a-z][a-z0-9-]{1,30}$
  runtime            TEXT NOT NULL DEFAULT 'auto',  -- auto|node|python|go|static|custom
  port               INTEGER UNIQUE,           -- NULL para static
  branch             TEXT NOT NULL DEFAULT 'main',
  build_cmd          TEXT,
  start_cmd          TEXT,
  health_path        TEXT,
  health_timeout_s   INTEGER NOT NULL DEFAULT 30,
  static_dir         TEXT,
  nice               INTEGER NOT NULL DEFAULT 10,
  desired_state      TEXT NOT NULL DEFAULT 'running',  -- running|stopped
  current_release_id INTEGER REFERENCES releases(id) ON DELETE SET NULL,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);

CREATE TABLE env_vars (
  app_id    INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  key       TEXT NOT NULL,                     -- ^[A-Za-z_][A-Za-z0-9_]*$
  value_enc BLOB NOT NULL,                     -- AES-256-GCM (nonce||ciphertext)
  is_secret INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (app_id, key)
);

CREATE TABLE domains (
  id         INTEGER PRIMARY KEY,
  app_id     INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  hostname   TEXT NOT NULL UNIQUE,
  is_primary INTEGER NOT NULL DEFAULT 0,
  auto       INTEGER NOT NULL DEFAULT 0,       -- 1 = <app>.<base_domain>
  created_at INTEGER NOT NULL
);

CREATE TABLE releases (
  id         INTEGER PRIMARY KEY,
  app_id     INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  git_sha    TEXT NOT NULL,
  git_ref    TEXT NOT NULL,
  message    TEXT,
  status     TEXT NOT NULL,                    -- building|ready|failed|pruned
  path       TEXT NOT NULL,
  size_bytes INTEGER,
  created_at INTEGER NOT NULL
);

CREATE TABLE deploys (
  id          INTEGER PRIMARY KEY,
  app_id      INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  release_id  INTEGER REFERENCES releases(id) ON DELETE SET NULL,
  trigger     TEXT NOT NULL,                   -- git_push|manual|rollback|rebuild
  status      TEXT NOT NULL,                   -- ver §7.2
  stage       TEXT,
  error       TEXT,
  log_path    TEXT NOT NULL,
  queued_at   INTEGER NOT NULL,
  started_at  INTEGER,
  finished_at INTEGER
);

CREATE TABLE events (
  id      INTEGER PRIMARY KEY,
  ts      INTEGER NOT NULL,
  level   TEXT NOT NULL,                       -- info|warn|error
  kind    TEXT NOT NULL,                       -- ex.: deploy.succeeded
  app_id  INTEGER REFERENCES apps(id) ON DELETE SET NULL,
  message TEXT NOT NULL,
  data    TEXT                                 -- JSON
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL                          -- segredos cifrados com a master key
);

CREATE INDEX idx_releases_app   ON releases(app_id, created_at DESC);
CREATE INDEX idx_deploys_app    ON deploys(app_id, queued_at DESC);
CREATE INDEX idx_events_ts      ON events(ts DESC);
CREATE INDEX idx_sessions_exp   ON sessions(expires_at);
```

### 7.2 Estados

**Deploy (`deploys.status`)**

| Status | Significado |
|---|---|
| `queued` | Na fila |
| `running` | Em execução (ver `stage`) |
| `succeeded` | Release ativada e saudável |
| `failed` | Falhou (antes ou depois da troca) |
| `rolled_back` | Falhou após a troca e a release anterior foi restaurada |
| `cancelled` | Cancelado pelo usuário |
| `interrupted` | `droidd` caiu durante a execução |

`stage` (quando `running`): `fetch → detect → install → build → prepare → switch → start → health → finalize`.

**App (estado observado, calculado — não persistido)**

| Estado | Condição |
|---|---|
| `running` | runit `run` e health ok |
| `starting` | runit `run`, ainda dentro da janela de health |
| `stopped` | `desired_state=stopped` e runit `down` |
| `crashed` | Crash loop detectado (§8.2.5) |
| `deploying` | Há deploy `running` para o app |
| `error` | Divergência não reparável (ex.: release ausente) |
| `unknown` | Falha ao consultar o runit |

---

## 8. Especificação dos componentes

### 8.1 `droidd` — ciclo de vida

1. **Boot:** carrega config → abre SQLite (WAL) → aplica migrações → carrega/gera `master.key` → recupera deploys `running` como `interrupted` e re-enfileira os `queued`.
2. **Sobe os servidores:** HTTP (painel/API) e socket Unix.
3. **Reconciliação inicial:** garante `dp-caddy`, serviços dos apps e config do Caddy.
4. **Laços em segundo plano:** worker de build (1 goroutine por slot), reconciliador (intervalo configurável), coletor de métricas leves (Fase 3), limpeza de sessões/logs.
5. **Desligamento (SIGTERM):** para de aceitar novas requisições, aguarda handlers (timeout 10 s), **não** para apps/Caddy/túnel, e deixa deploys `running` como `interrupted` (recuperados no próximo boot).

**Requisitos de implementação:** `GOMEMLIMIT` configurado (ex.: 64 MiB) por padrão; logs estruturados (`log/slog`); nenhuma goroutine sem `context`.

### 8.2 Gerenciador de serviços (runit)

Interface (permite *fake* em testes no PC — RNF-13):

```go
type ServiceManager interface {
    Ensure(ctx context.Context, spec ServiceSpec) error  // cria/atualiza dir do serviço
    Remove(ctx context.Context, name string) error
    Up(ctx context.Context, name string) error
    Down(ctx context.Context, name string) error
    Restart(ctx context.Context, name string, wait time.Duration) error
    Status(ctx context.Context, name string) (ServiceStatus, error)
}

type ServiceStatus struct {
    State    string        // run | down | finish | unknown
    PID      int
    Uptime   time.Duration
    WantsUp  bool
}
```

#### 8.2.1 Criação do serviço (atômica)

1. Montar o diretório em `$SVDIR/.dp-tmp-<nome>/` com `run`, `log/run` e o arquivo `down` (para não iniciar antes da hora).
2. `rename` para `$SVDIR/dp-app-<nome>/` (atômico no mesmo filesystem).
3. Aguardar o `runsvdir` detectar o serviço (hipótese: poucos segundos — V-02); timeout de 15 s.
4. `Up` remove o arquivo `down` e executa `sv up`.

#### 8.2.2 Script `run` gerado

Ver Apêndice A.1. Pontos obrigatórios: `exec 2>&1`; `cd` no `current`; `PATH` explícito; `exec chpst -e <envdir> -n <nice> sh <start.sh>` — o `chpst` do Termux **não executa scripts com shebang** ("file does not exist", achado da Fase 2), por isso o `start.sh` roda via `sh`; dentro dele o comando do app usa `exec`, de modo que o processo final substitui o shell e os sinais chegam ao app.

#### 8.2.3 Parada e reinício

`sv -w <s> stop|restart <serviço>`: envia TERM e aguarda; se exceder o tempo, `sv kill`. Tempo padrão de graça: 10 s.

#### 8.2.4 Logs de runtime

`log/run` usa `svlogd -tt <logs/apps/<app>>`; arquivo `config` do svlogd com tamanho máx. e quantidade de arquivos (padrão: 1 MB × 5; configurável). O `droidd` lê `current` para *tail*/*follow* (polling a cada 500 ms; não depender de inotify).

#### 8.2.5 Detecção de crash loop (RF-20)

- A cada ciclo do reconciliador, ler `Status`. Se o `Uptime` do app for baixo e o número de reinícios na janela (`crash_loop_window`) ≥ `crash_loop_threshold`, então:
  1. Criar `down` (para o runit parar de reiniciar).
  2. Marcar estado observado `crashed` e registrar evento `app.crash_loop` com as últimas linhas de log.
- Sair de `crashed` exige ação explícita (Start/Restart/novo deploy).
- *Detalhe de implementação a validar:* como contar reinícios (ler `supervise/stat` + `supervise/pid` ou rastrear mudanças de PID entre ciclos).

### 8.3 Sistema de build

#### 8.3.1 Detecção de runtime

Ordem (primeira correspondência vence, a menos que o runtime esteja fixado):

| Prioridade | Evidência | Runtime |
|---|---|---|
| 1 | `droidpaas.toml` com `runtime` | conforme declarado |
| 2 | `package.json` | `node` |
| 3 | `requirements.txt` ou `pyproject.toml` | `python` |
| 4 | `go.mod` | `go` |
| 5 | `index.html` na raiz ou `static_dir` declarado | `static` |
| 6 | `Procfile` com `web:` | `custom` |
| — | Nenhuma | Erro claro de detecção, com instruções |

#### 8.3.2 Pipelines por runtime

| Runtime | install | build | start (padrão) | Observações |
|---|---|---|---|---|
| **node** | `npm ci` (ou `pnpm`/`yarn` se houver lockfile correspondente); se não houver lockfile, `npm install` com aviso | `npm run build` se existir script `build`; depois `npm prune --omit=dev` | `npm start` (script) ou `node <main>` | `NODE_ENV=production`; `NODE_OPTIONS=--max-old-space-size=<limite>` durante o build; cache em `cache/npm` |
| **python** | `python -m venv .venv` + `pip install -r requirements.txt` (ou `pip install .`) | — | `Procfile`/`start` obrigatório (não há convenção segura) | Dependências nativas podem falhar (C-04); PoC: `pip install numpy` (sem wheel) não terminou em 10 min; erro deve apontar o pacote problemático |
| **go** | — | `go build -o app ./...` (alvo `main` detectado) | `./app` | Build no aparelho consome RAM; avisar |
| **static** | — | `build` opcional | — (Caddy serve `static_dir` ou raiz) | Sem processo, sem porta |
| **custom** | `install` opcional | `build` opcional | `Procfile` `web:` ou `start` | Comandos definidos pelo usuário (admin confiável) |

**Segurança de execução:** comandos são executados como **vetor de argumentos** (sem interpolar ref/branch/sha em shell). `build_cmd` e `start_cmd` definidos pelo admin são tratados como script, via `sh -c`, mas **nunca** compostos com dados não confiáveis.

#### 8.3.3 Fila e worker

- Tabela `deploys` é a fila persistente. Worker(s) em número `build.max_concurrent` (padrão **1**).
- Cada build roda em um **grupo de processos próprio** com `nice` configurado; cancelamento envia SIGTERM ao grupo e, após 10 s, SIGKILL. **No Android 13 o `nice` não tem efeito (V-09 refutada no PoC): é best-effort.** A proteção real é a fila de 1 build, `NODE_OPTIONS=--max-old-space-size` e `GOMAXPROCS`.
- Timeout total do build: `build.timeout` (padrão 15 min).
- Saída (stdout+stderr) vai para `logs/deploys/<app>/<id>.log` **e** é transmitida a quem estiver acompanhando (git push, SSE).
- *Limitação conhecida:* sem cgroups, não há limite rígido de memória; mitigação por flags do runtime e fila de 1.

#### 8.3.4 Armazenamento de releases

- Diretório: `apps/<app>/releases/<unix>-<sha7>`.
- Código obtido com `git archive <sha> | tar -x` (sem `.git` na release).
- Após ativar uma release com sucesso, aplicar a retenção: manter as `keep_releases` mais recentes `ready` + a atual; remover as demais (`status=pruned`).

### 8.4 Git (SSH e HTTP)

#### 8.4.1 Repositório

Ao criar o app, `git init --bare apps/<app>/repo.git` e instalar o hook `post-receive` (Apêndice A.4).

#### 8.4.2 Acesso por SSH

- O `sshd` do Termux (porta padrão 8022 — a confirmar) autentica por chave pública.
- O `droidd` mantém um **bloco gerenciado** em `authorized_keys` (entre marcadores `# BEGIN DROIDPAAS` / `# END DROIDPAAS`), uma linha por chave cadastrada:

```
command="droid git-shell --key 7",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... nome
```

- `droid git-shell` lê `SSH_ORIGINAL_COMMAND`, **aceita somente** `git-receive-pack '<app>.git'` e `git-upload-pack '<app>.git'`, valida o nome do app contra o regex, resolve o caminho do `repo.git` e executa o comando git correspondente. Qualquer outra coisa → erro e exit 1.
- O conteúdo fora do bloco gerenciado **nunca** é tocado.
- Sujeito a V-06.

#### 8.4.3 Acesso por HTTP (RF-05, Should)

- Rota `/git/<app>.git/*` no `droidd`, delegando a `git http-backend` (via `net/http/cgi`), com autenticação **HTTP Basic** (usuário qualquer + token de API com escopo `git`).
- Permite `git push` através do túnel Cloudflare, sem SSH. Sujeito a V-07.

#### 8.4.4 Hook `post-receive`

1. Lê linhas `<old> <new> <ref>` do stdin.
2. Considera apenas `refs/heads/<branch configurado>`; outras refs são ignoradas com mensagem.
3. Executa `droid hook post-receive --app <nome> --socket <caminho>` (as linhas `old new ref` seguem pelo stdin; o filtro de branch é feito pelo `droidd`), que conecta ao socket Unix, solicita o deploy e **transmite a saída do build** para o stderr do hook (aparece no terminal como `remote: ...`).
4. O código de saída do hook reflete o resultado do deploy; falha de deploy **não** desfaz o push (o commit já foi recebido), mas o terminal mostra o erro e o app permanece na release anterior.

### 8.5 Proxy (Caddy)

#### 8.5.1 Bootstrap

O instalador cria `dp-caddy` executando `caddy run --config proxy/bootstrap.json`, com Admin API em `127.0.0.1:12019`, HTTPS automático **desativado** e um servidor em `127.0.0.1:18080` com rota padrão 404 (Apêndice A.5).

#### 8.5.2 Geração da configuração

Para cada app, a partir de `domains`:

| Tipo | Rota |
|---|---|
| Processo | `match host in [...]` → `reverse_proxy 127.0.0.1:<port>` |
| Estático | `match host in [...]` → `file_server` com `root = releases/<atual>/<static_dir>` |
| App parado | `match host` → resposta estática 503 (página do DroidPaaS) |
| Fallback | 404 (página do DroidPaaS) |
| Painel (opcional) | `match host = panel_hostname` → `reverse_proxy 127.0.0.1:8090` |

#### 8.5.3 Aplicação

- Serializar a config completa → `POST http://127.0.0.1:12019/load`.
- Calcular hash da config; **só aplicar se mudou** (evita recargas inúteis).
- Em erro do Caddy: registrar evento `proxy.apply_failed`, manter a config anterior e tentar de novo no próximo ciclo (com *backoff*).
- Reaplicar no boot do `droidd` e sempre que o reconciliador detectar `dp-caddy` reiniciado.

### 8.6 Túnel (`cloudflared`)

- **Modo token** (Fase 2, Should): o usuário cria o túnel no painel da Cloudflare, cola o token em `droid tunnel set --token ...` (ou nas Configurações, na Fase 3). O `droidd` cria `dp-cloudflared` executando `cloudflared tunnel run --token <token>` (token lido de arquivo/ambiente do serviço, nunca na linha de comando visível em `ps` — **forma confirmada no PoC: variável `TUNNEL_TOKEN` via envdir (`chpst -e`) ou `--token-file` com arquivo `0600`; nunca `--token` na linha de comando**).
- **Roteamento (ADR-06):** na Cloudflare, o usuário configura um hostname público (idealmente wildcard `*.<base_domain>`, ver V-05) apontando para `http://127.0.0.1:18080`. O DroidPaaS **não** altera a configuração remota.
- **Domínios fora do wildcard:** o usuário cria manualmente o hostname no túnel; o DroidPaaS só adiciona a rota no Caddy (e exibe uma dica na UI).
- O token é armazenado cifrado em `settings`.
- Estado exibido: `connected`/`down`, conforme o serviço runit e logs do `cloudflared`.

### 8.7 Reconciliador

Laço a cada `reconcile.interval`:

1. Carregar estado desejado (apps, domínios, configurações).
2. Para cada app: garantir diretório de serviço (`Ensure`), `up/down` conforme `desired_state`, e atualizar o estado observado em memória (cache para a API/painel).
3. Garantir `dp-caddy` ativo e config aplicada (hash).
4. Se há token de túnel, garantir `dp-cloudflared`.
5. Detectar crash loop (§8.2.5).
6. Remover serviços `dp-app-*` órfãos (apps removidos) — com confirmação por *grace period* para evitar apagar em caso de falha de leitura.

**Propriedade:** idempotente; qualquer passo pode falhar sem corromper o estado.

### 8.8 Variáveis de ambiente

- Armazenamento: `env_vars.value_enc` com **AES-256-GCM** usando `master.key` (nonce aleatório por valor).
- Materialização: a cada deploy e a cada edição, o `droidd` regenera `apps/<app>/env/` (um arquivo por variável, `0600`, escrita atômica).
- **Variáveis padrão injetadas** (não editáveis, sobrescrevem o usuário): `PORT`, `HOST=127.0.0.1`, `DP_APP`, `DP_RELEASE`, `DP_DATA_DIR`, e `NODE_ENV=production` para `node`.
- Alterar variáveis **não** reinicia automaticamente; a UI/CLI oferece "Salvar e reiniciar".
- Valores `is_secret` são mascarados em respostas da API/UI (`••••`), exceto em endpoint explícito de revelação (com auditoria).
- **Limitação honesta:** como todos os processos compartilham o UID, a cifragem protege contra vazamento do arquivo de banco/backup, **não** contra código malicioso rodando no mesmo usuário.

### 8.9 Alocação de portas

- Intervalo configurável (`ports.range_*`). Ao criar app de processo, escolher a **menor porta livre** e persistir (`apps.port UNIQUE`); a porta é estável durante a vida do app.
- Antes de usar, testar se a porta está livre (tentativa de `listen` e fechamento).

### 8.10 Métricas do sistema (leitura)

| Fonte | Dado | Fase |
|---|---|---|
| `termux-battery-status` (JSON) | % bateria, temperatura, carregando, saúde | 3 |
| `/proc/meminfo` | RAM total/disponível | 3 |
| `/proc/<pid>/status` | RSS por app (soma da árvore a partir do PID do `runsv`) | 3 |
| `/proc/<pid>/stat` (`/proc/stat` e `/proc/loadavg` são **negados** no Android 13) | CPU por app (delta de utime+stime sobre o tempo de relógio) | 3 |
| `statfs` em `$DP_HOME` | Uso de disco | 3 |

- Coleta com intervalo leve (padrão 30 s) e cache em memória; histórico curto (ex.: últimas 2 h em *ring buffer*), **sem** gravar série temporal no SQLite na Fase 3.
- Chamar `termux-battery-status` com *timeout* (ex.: 5 s), pois depende do app Termux:API.
- **Esta coleta é somente leitura.** Nenhuma ação automática é tomada na Fase 3; a política (Fase 4) usará a mesma interface `DeviceProbe`.

```go
type DeviceProbe interface {
    Battery(ctx context.Context) (BatteryInfo, error) // pct, tempC, charging, health
    Memory(ctx context.Context) (MemInfo, error)
    Disk(ctx context.Context, path string) (DiskInfo, error)
    ProcessStats(ctx context.Context, rootPID int) (ProcStats, error)
}
```

---

## 9. Fluxos detalhados

### 9.1 Primeira instalação (`droidd init`)

1. Verificar pacotes obrigatórios (`git`, `openssh`, `caddy`, `runit`/`termux-services`, `termux-api`, `jq` se usado) e instruir `pkg install` do que faltar (**não** instalar sem confirmação). Detectar também portas já ocupadas (ex.: 8080/2019 de outro Caddy), processos/serviços `cloudflared` duplicados e scripts de boot existentes: **avisar, nunca sobrescrever**.
2. Criar a árvore de diretórios (§6.1) com permissões restritas.
3. Gerar `droidd.toml` padrão e `master.key` (32 bytes aleatórios, `0600`).
4. Criar banco e aplicar migrações.
5. Solicitar usuário/senha do admin (senha mínima de 12 caracteres) e gravar o hash.
6. Gerar o **primeiro token de API** e exibi-lo **uma única vez**.
7. Criar os serviços runit `droidd` e `dp-caddy`; escrever o script de boot em **arquivo próprio** `~/.termux/boot/droidpaas` (Apêndice A.7), sem alterar scripts de boot existentes do usuário.
8. Exibir o checklist pós-instalação: desativar o *phantom process killer* (adb), isenção de bateria, configurar o túnel e o domínio base.

### 9.2 Criação de app

1. Validar o nome (`^[a-z][a-z0-9-]{1,30}$`, fora da lista reservada: `droidd`, `caddy`, `panel`, `admin`, `api`, `git`, `www`).
2. Inserir em `apps`; alocar porta (se não for estático).
3. `git init --bare`, instalar hook; criar `env/`, `shared/`.
4. Criar domínio automático `<nome>.<base_domain>` (se houver domínio base).
5. Criar o serviço runit (parado, com `down`) e atualizar o Caddy (rota 503 "sem release").
6. Responder com a URL de remote Git (SSH e/ou HTTP).

### 9.3 Deploy por `git push` (caminho feliz)

```
git push droid main
 └─► sshd ─► droid git-shell ─► git-receive-pack (repo.git)
        └─► post-receive ─► droid hook ─► (socket) droidd
              1. cria deploy (queued) e responde com stream de logs
              2. worker pega o deploy → status=running
              3. fetch:   git archive <sha> → releases/<ts>-<sha7>
              4. detect:  runtime, comandos, porta, health_path
              5. install: dependências (nice, timeout)
              6. build:   se aplicável
              7. prepare: grava start.sh e regenera envdir
              8. switch:  current → nova release (symlink atômico)
              9. start:   sv restart (release anterior parada com TERM)
             10. health:  GET/TCP em 127.0.0.1:<port> até health_timeout
             11. finalize: status=succeeded, apps.current_release_id, GC, evento
```

**Falha antes de `switch` (etapas 3–7):** release marcada `failed`; `current` e o processo em execução **não** são tocados (RNF-09).

**Falha em `start`/`health` (etapas 9–10):** restaurar o symlink para a release anterior, `sv restart`, confirmar health; status do deploy = `rolled_back` (ou `failed` se não havia release anterior → app fica `stopped`/`error`).

### 9.4 Rollback manual

1. Escolher release anterior `ready` (padrão: a imediatamente anterior).
2. Criar deploy `trigger=rollback` (sem build): `switch` → `start` → `health`.
3. Atualizar `apps.current_release_id`; registrar evento.

### 9.5 Re-deploy / rebuild

- **Re-deploy** da última ref: cria deploy `manual` com o último `git_sha` conhecido do branch (`rev-parse` no `repo.git`).
- **Rebuild**: igual, mas força reinstalação de dependências (ignora caches do app).

### 9.6 Remoção de app

1. Parar e remover o serviço runit; remover rotas do Caddy.
2. Remover registros do banco (cascata) e diretórios `apps/<app>/` e `logs/` — **somente após confirmação explícita** (`--yes` na CLI; confirmação digitando o nome na UI).
3. Registrar evento.

### 9.7 Boot do aparelho

1. Android inicia → Termux:Boot executa o script (wake-lock + `runsvdir`).
2. `runsvdir` sobe os serviços marcados como ativos (sem `down`): `droidd`, `dp-caddy`, `dp-cloudflared`, `dp-app-*`, `sshd`.
3. `droidd` reconcilia e reaplica a config do Caddy; recupera deploys interrompidos.

---

## 10. API REST

### 10.1 Convenções

- Base: `/api/v1`. JSON UTF-8. Datas em RFC 3339 (UTC) nas respostas.
- Autenticação: `Authorization: Bearer <token>` (CLI/automação) **ou** cookie de sessão (painel, com CSRF — Fase 3).
- Erros:

```json
{ "error": { "code": "app_not_found", "message": "App 'x' não existe", "details": {} } }
```

- Códigos HTTP: 200/201/204; 400 (validação), 401, 403, 404, 409 (conflito), 422, 429 (rate limit), 500.
- Paginação: `?limit=` (padrão 50, máx. 200) e `?before=<id>`.
- Streams de logs: `text/event-stream` (SSE).

### 10.2 Endpoints

| Método | Rota | Descrição | Fase |
|---|---|---|---|
| POST | `/auth/login` | Login (sessão) | 3 |
| POST | `/auth/logout` | Logout | 3 |
| GET | `/me` | Usuário atual | 2 |
| POST | `/me/password` | Trocar senha | 3 |
| GET | `/apps` | Listar apps (com estado observado) | 2 |
| POST | `/apps` | Criar app | 2 |
| GET | `/apps/{name}` | Detalhe | 2 |
| PATCH | `/apps/{name}` | Editar (branch, comandos, health, nice, runtime) | 2 |
| DELETE | `/apps/{name}?confirm={name}` | Remover | 2 |
| POST | `/apps/{name}/start` | Iniciar | 2 |
| POST | `/apps/{name}/stop` | Parar | 2 |
| POST | `/apps/{name}/restart` | Reiniciar | 2 |
| GET | `/apps/{name}/env` | Listar variáveis (segredos mascarados) | 2 |
| PUT | `/apps/{name}/env` | Substituir/mesclar variáveis (corpo em lote) | 2 |
| DELETE | `/apps/{name}/env/{key}` | Remover variável | 2 |
| GET | `/apps/{name}/domains` | Listar domínios | 2 |
| POST | `/apps/{name}/domains` | Adicionar domínio | 2 |
| DELETE | `/apps/{name}/domains/{hostname}` | Remover domínio | 2 |
| GET | `/apps/{name}/deploys` | Histórico de deploys | 2 |
| POST | `/apps/{name}/deploys` | Disparar deploy (`{"ref":"main","rebuild":false}`) | 2 |
| POST | `/apps/{name}/rollback` | Rollback (`{"release_id":N}` opcional) | 2 |
| GET | `/deploys/{id}` | Detalhe do deploy | 2 |
| GET | `/deploys/{id}/logs` | Log do build (SSE ou texto) | 2 |
| POST | `/deploys/{id}/cancel` | Cancelar | 2 |
| GET | `/apps/{name}/logs?tail=200&follow=1` | Logs de runtime (SSE se `follow`) | 2 |
| GET | `/apps/{name}/stats` | RSS/CPU atual e recente | 3 |
| GET/POST/DELETE | `/ssh-keys`, `/ssh-keys/{id}` | Chaves SSH | 2 |
| GET/POST/DELETE | `/tokens`, `/tokens/{id}` | Tokens de API | 2 |
| GET/PUT | `/settings` | Configurações globais | 2 |
| PUT | `/settings/tunnel` | Definir/limpar token do túnel | 2 |
| GET | `/system` | Versões, uptime, serviços base, apps por estado e túnel (RAM/disco/bateria entram na Fase 3) | 2 |
| GET | `/apps/{name}/releases` | Releases do app e a atual | 2 |
| GET | `/backup[?key=1]` | Cópia consistente do SQLite (`.tar.gz` com a `master.key` se `key=1`; auditado) | 2 |
| * | `/git/{app}.git/*` | Git smart HTTP (Basic: senha = token `git`/`full`; `git http-backend` via CGI) | 2 |
| GET | `/events?limit=` | Eventos | 2 |
| GET | `/healthz` | Liveness (sem autenticação, sem dados sensíveis) | 2 |

### 10.3 Exemplos

**Criar app**

```http
POST /api/v1/apps
Authorization: Bearer dp_...
Content-Type: application/json

{ "name": "minha-api", "runtime": "auto", "branch": "main" }
```

```json
{
  "name": "minha-api",
  "runtime": "auto",
  "port": 20001,
  "state": "stopped",
  "domains": ["minha-api.apps.exemplo.com"],
  "git": {
    "ssh":  "ssh://u0_a123@<host>:8022/minha-api.git",
    "http": "https://<painel>/git/minha-api.git"
  }
}
```

**Variáveis em lote**

```http
PUT /api/v1/apps/minha-api/env
{ "set": { "DATABASE_URL": {"value":"...","secret":true}, "LOG_LEVEL": {"value":"info"} },
  "unset": ["OLD_VAR"] }
```

**SSE de logs**

```
event: line
data: {"ts":"2026-10-09T22:10:01Z","text":"server listening on 20001"}
```

### 10.4 Socket Unix interno (não público)

| Método | Rota | Uso |
|---|---|---|
| POST | `/internal/hook/post-receive` | Chamado por `droid hook`; resposta em streaming (texto) |
| GET | `/internal/ping` | Diagnóstico da CLI |

Permissões do socket `0600`. Não exposto via HTTP/TCP.

---

## 11. CLI `droid`

Subcomandos (implementação com a biblioteca padrão; sem framework pesado):

| Comando | Descrição |
|---|---|
| `droidd init` | Inicialização (§9.1) |
| `droidd serve` | Executa o daemon (usado pelo runit) |
| `droid login --url <url> --token <t>` | Salva credenciais em `~/.config/droid/config.toml` (0600) |
| `droid apps` / `droid apps create <nome>` / `droid apps rm <nome> --yes` | Gestão de apps |
| `droid start\|stop\|restart <app>` | Controle |
| `droid deploy <app> [--ref main] [--rebuild]` | Deploy manual |
| `droid rollback <app> [--release N]` | Rollback |
| `droid logs <app> [-f] [-n 200]` | Logs de runtime |
| `droid deploys <app>` / `droid deploy-logs <id> [-f]` | Histórico e logs de build |
| `droid env <app> list\|set K=V\|unset K` | Variáveis |
| `droid domains <app> add\|rm <host>` | Domínios |
| `droid keys add <arquivo.pub>` / `droid keys ls` / `droid keys rm <id>` | Chaves SSH |
| `droid tokens create <nome>` / `ls` / `rm` | Tokens |
| `droid tunnel set --token <t>` | Configura o túnel |
| `droid status` | Resumo do sistema |
| `droid backup [--out arquivo]` | Backup do SQLite (cópia consistente) |
| `droid git-shell`, `droid hook ...` | **Internos** (usados pelo sshd e pelo hook) |

Saída humana por padrão; `--json` para scripts. Códigos de saída: 0 sucesso; 1 erro genérico; 2 uso incorreto; 3 falha de autenticação.

---

## 12. Painel web (Fase 3)

### 12.1 Princípios de UX

- **Mobile-first:** o usuário vai operar pelo próprio celular e por outros dispositivos; alvos de toque ≥ 44 px; navegação por menu recolhível.
- **Familiar para quem usa o Dokploy:** barra lateral, abas por app, histórico de deploys com *badges* de status, botão de ação principal visível.
- **Leve:** HTML renderizado no servidor; htmx para interações parciais; JavaScript próprio mínimo (apenas SSE/atalhos); **sem CDN** (assets embutidos).
- **Estado sempre visível:** cada app mostra status, última release, URL e uso de recursos.
- **Ações destrutivas** exigem confirmação (digitar o nome do app).

### 12.2 Mapa de páginas

| Rota | Conteúdo |
|---|---|
| `/login` | Formulário de login |
| `/` | Dashboard: cartões de apps + widget do dispositivo |
| `/apps/new` | Assistente de criação |
| `/apps/{name}` | Visão geral do app (aba padrão) |
| `/apps/{name}/deployments` | Histórico e ações de deploy |
| `/apps/{name}/deployments/{id}` | Log de build ao vivo/arquivado |
| `/apps/{name}/environment` | Variáveis |
| `/apps/{name}/logs` | Logs de runtime ao vivo |
| `/apps/{name}/domains` | Domínios e instruções de DNS/túnel |
| `/apps/{name}/settings` | Branch, comandos, health, nice, runtime, zona de perigo |
| `/settings` | Geral · Túnel · Chaves SSH · Tokens · Conta |
| `/events` | Linha do tempo de eventos |

### 12.3 Wireframes

**Dashboard (celular)**

```
┌──────────────────────────────┐
│ ☰  DroidPaaS        [+ App]  │
├──────────────────────────────┤
│ Dispositivo                  │
│ 🔋 87% · 36,5 °C · carregando │
│ RAM livre 1,1 GB · Disco 62% │
├──────────────────────────────┤
│ ● minha-api         running  │
│   v 3f2a9c1 · há 2 h         │
│   minha-api.apps.exemplo.com │
│   RSS 58 MB · CPU 1%         │
├──────────────────────────────┤
│ ● blog              running  │
│   estático · 8c1d0e2         │
├──────────────────────────────┤
│ ○ worker-teste      stopped  │
└──────────────────────────────┘
```

**Página do app (desktop)**

```
┌───────────┬──────────────────────────────────────────────────────┐
│ DroidPaaS │ minha-api  ● running     [Deploy] [Reiniciar] [Parar]│
│           ├──────────────────────────────────────────────────────┤
│ Apps      │ Visão geral | Deploys | Ambiente | Logs | Domínios | ⚙ │
│ Eventos   ├──────────────────────────────────────────────────────┤
│ Config.   │ Release atual   3f2a9c1  "fix: timeout" (há 2 h)     │
│           │ URL             https://minha-api.apps.exemplo.com   │
│           │ Runtime         node · porta 20001                   │
│           │ Recursos        RSS 58 MB · CPU 1% · uptime 2 h 14   │
│           │ Git             ssh://u0_a123@host:8022/minha-api.git│
│           │                                                      │
│           │ Últimos deploys                                      │
│           │ #14 ✔ succeeded  3f2a9c1  git_push   48 s            │
│           │ #13 ✖ failed     b71e00a  git_push   (npm ci)        │
│           │ #12 ⟲ rolled_back 91cc2de manual                     │
└───────────┴──────────────────────────────────────────────────────┘
```

### 12.4 Comportamento por tela

**Dashboard**
- Lista de apps com *badge* de estado (`running`, `starting`, `stopped`, `crashed`, `deploying`, `error`).
- *Polling* leve via htmx (`hx-trigger="every 10s"`) apenas nos blocos de estado e no widget do dispositivo.
- Widget do dispositivo: bateria %, temperatura, carregando, RAM disponível, disco. Se o Termux:API não responder, exibir "indisponível" sem erro. **Sem ações automáticas** (Fase 4).

**Assistente de criação (3 passos)**
1. Nome e branch → 2. Runtime (auto/manual) e comandos opcionais → 3. Resumo, URL do remote Git e instruções de `git remote add`/`git push`. Chaves SSH ausentes geram um aviso com link para `/settings`.

**Deploys**
- Lista paginada; clique abre o log. Log de build em andamento via SSE com rolagem automática (pausável) e botão **Cancelar**.
- Botões: **Deploy** (re-deploy da ref atual), **Rebuild**, **Rollback** (em releases `ready`).

**Ambiente**
- Tabela chave/valor; segredos mascarados com botão "revelar" (ação auditada). Edição em lote com **Salvar** e **Salvar e reiniciar**.

**Logs**
- Últimas 200 linhas + *follow* via SSE; filtro de texto no cliente; botão de download das últimas N linhas.

**Domínios**
- Lista; adicionar hostname; mostra o que falta (DNS/túnel) com texto de ajuda. O domínio automático não pode ser removido (pode ser desativado em Configurações).

**Configurações do app**
- Campos editáveis (branch, `build`, `start`, `health_path`, `health_timeout`, `nice`, runtime) e **zona de perigo** (remover app, com confirmação digitando o nome).

**Configurações globais**
- *Geral:* domínio base, retenção de releases, Git HTTP.
- *Túnel:* colar/limpar token, estado do `cloudflared`, instruções do wildcard.
- *Chaves SSH:* adicionar/remover (mostra fingerprint).
- *Tokens:* criar (exibir uma vez), revogar.
- *Conta:* trocar senha, encerrar outras sessões.

### 12.5 Implementação do front-end

| Aspecto | Decisão |
|---|---|
| Renderização | `html/template` com *layouts* e *partials* |
| Interatividade | htmx (arquivo vendorizado e embutido via `embed`) |
| Tempo real | SSE nativo (`EventSource`); reconexão automática; `Last-Event-ID` para retomar |
| CSS | Um único CSS escrito à mão com variáveis (tema claro/escuro por `prefers-color-scheme` + alternância manual) |
| JS próprio | Pequeno (< ~5 KB): auto-scroll de logs, confirmação de exclusão, alternância de tema |
| Formulários | Funcionam sem JS quando possível (degradação graciosa) |
| Acessibilidade | Semântica HTML, foco visível, contraste adequado, rótulos em todos os controles |
| i18n | Português (pt-BR) na Fase 3; textos centralizados para futura tradução |

### 12.6 Segurança específica do painel

- Cookie de sessão `HttpOnly`, `SameSite=Lax`, `Secure` quando a requisição chega via HTTPS (cabeçalho `X-Forwarded-Proto` **somente** de proxies confiáveis).
- **CSRF:** token por sessão exigido em toda requisição não-GET (campo oculto/cabeçalho `X-CSRF-Token` pelo htmx) **e** verificação de `Origin`/`Host`.
- Cabeçalhos: `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`.
- Sem scripts inline (para manter a CSP restritiva).
- Escapar toda saída de log (logs são **não confiáveis**: podem conter HTML/ANSI); converter ANSI para HTML seguro ou removê-lo.

---

## 13. Segurança

### 13.1 Modelo de ameaças

| ID | Ameaça | Mitigação | Risco residual |
|---|---|---|---|
| T-01 | Acesso externo ao painel/API | Bind em `127.0.0.1`; exposição só por túnel com Cloudflare Access ou Tailscale; autenticação forte; rate limit | Médio, se o usuário expuser sem Access |
| T-02 | Força bruta no login | Limite de 5 falhas/5 min por IP+usuário, atraso progressivo, evento de auditoria | Baixo |
| T-03 | Código de um app lê dados de outros apps | **Não mitigável** sem isolamento (C-06) | Aceito: só código confiável |
| T-04 | Vazamento do arquivo de banco/backup | Segredos cifrados (AES-GCM); permissões `0600` | Baixo-médio (chave no mesmo aparelho) |
| T-05 | Abuso de chave SSH | `command=` forçado, sem PTY/forwarding, somente git para apps válidos | Baixo |
| T-06 | Injeção de comandos / *path traversal* via nomes, refs, hostnames | Validação rígida por regex; exec com vetor de argumentos; resolução de caminhos com verificação de prefixo | Baixo |
| T-07 | XSS via logs ou nomes | Escape de saída, CSP, remoção de ANSI | Baixo |
| T-08 | CSRF | SameSite + token + checagem de Origin | Baixo |
| T-09 | Token de API vazado | Armazenar só hash; escopos; revogação; exibição única | Médio |
| T-10 | Dependências maliciosas (supply chain) nos apps | Fora do controle do DroidPaaS; builds com timeout; aviso na documentação | Aceito |
| T-11 | Aparelho perdido/roubado | Criptografia de armazenamento do Android; bloqueio de tela | Fora do escopo do software |
| T-12 | Esgotamento de recursos (DoS por app ou build) | Fila de 1 build, timeout, nice; limites rígidos só na Fase 4 | Médio |

### 13.2 Autenticação e segredos

| Item | Especificação |
|---|---|
| Senha do admin | argon2id (parâmetros moderados; validar tempo ≤ ~500 ms no M22); mínimo 12 caracteres |
| Sessão | Token aleatório de 32 bytes no cookie; no banco só o SHA-256; expiração: 12 h de inatividade / 7 dias absolutos |
| Token de API | `dp_` + 32 bytes aleatórios (base32/base64url); exibido uma vez; no banco só SHA-256 + prefixo |
| Escopos de token | `full` (padrão) e `git` (apenas Git HTTP) |
| Segredos em repouso | AES-256-GCM com `master.key` (`0600`); rotação de chave: fora de escopo |
| Arquivos sensíveis | `master.key`, `env/*`, `droidpaas.db`, `run/droidd.sock`: `0600`/diretórios `0700` |
| Comparações | Tempo constante para tokens/hashes |

### 13.3 Rede

| Serviço | Endereço padrão | Observação |
|---|---|---|
| Painel/API | `127.0.0.1:8090` | Alterar para outra interface é decisão explícita do usuário (aviso na UI) |
| Caddy | `127.0.0.1:18080` | Entrada apenas do túnel/local |
| Admin do Caddy | `127.0.0.1:12019` | Nunca exposto |
| Apps | `127.0.0.1:<porta>` | Instruir apps a ouvir em `HOST` |
| sshd | porta do Termux (padrão 8022) | Preferir Tailscale/LAN; não expor diretamente à internet |

### 13.4 Auditoria

Registrar em `events`: login (sucesso/falha), criação/revogação de tokens e chaves, criação/remoção de apps, deploys, rollback, revelação de segredos, mudança de configurações, falhas do reconciliador. Eventos **não** devem conter valores de segredos.

---

## 14. Observabilidade e operação

### 14.1 Logs

| Origem | Destino | Retenção |
|---|---|---|
| `droidd` | stderr → `log/run` do runit (svlogd) em `logs/droidd/` | Rotação por tamanho |
| Apps (runtime) | svlogd em `logs/apps/<app>/` | 1 MB × 5 (configurável) |
| Build | `logs/deploys/<app>/<id>.log` | `keep_logs_days` (padrão 30) |

### 14.2 Saúde

- `/healthz` (liveness do `droidd`).
- `droid status` e `/system`: versões, uptime, estado dos serviços base (`droidd`, `dp-caddy`, `dp-cloudflared`, `sshd`).
- Eventos de falha (crash loop, falha de proxy, deploy falho) visíveis na página de eventos; **notificações push/Termux** ficam para a Fase 4 (pode-se prever o gancho `Notifier`).

### 14.3 Backup e atualização

- `droid backup`: cópia consistente do SQLite (`VACUUM INTO` ou API de backup) + `master.key` em pacote opcional (com aviso de sensibilidade).
- Atualização: substituir o binário e reiniciar via `sv restart droidd`; migrações aplicam-se no boot, com **backup automático do banco antes de migrar**.
- Código e dados dos apps (`shared/`) **não** estão no backup do banco (documentar).

### 14.4 Checklist operacional do aparelho (documentação entregue com o projeto)

- Isentar o Termux da otimização de bateria; fixá-lo nos recentes.
- Desativar o *phantom process killer* via adb sem fio (procedimento descrito na Fase 1).
- Limitar a carga (80–85%) se o aparelho oferecer; monitorar inchaço da bateria.
- Manter ventilação; evitar capa; preferir Wi-Fi.

---

## 15. Estrutura do repositório e stack

### 15.1 Stack

| Camada | Escolha | Observação |
|---|---|---|
| Linguagem | Go ≥ 1.22 (confirmado: 1.27.1 no Termux, `GOOS=android`) | Build no Termux |
| HTTP | `net/http` (stdlib, `ServeMux` com padrões) | Evita dependências |
| Banco | SQLite via `modernc.org/sqlite` (V-03) | Fallback CGO |
| Migrações | SQL embutido + tabela `schema_migrations` | Sem ferramenta externa |
| Config | TOML (`pelletier/go-toml/v2` ou similar) | |
| Cripto | `golang.org/x/crypto` (argon2) + stdlib (AES-GCM, SHA-256) | |
| Logs | `log/slog` | |
| Templates | `html/template` + `embed` | |
| Front-end | htmx (vendorizado) + CSS próprio | |
| Testes | `testing`, `httptest`, *golden files* | |

### 15.2 Árvore do repositório

```
droidpaas/
├── cmd/
│   ├── droidd/            # daemon
│   └── droid/             # CLI (+ subcomandos internos git-shell/hook)
├── internal/
│   ├── api/               # handlers REST, SSE, socket interno
│   ├── apps/
│   ├── auth/
│   ├── build/             # detectores e pipelines
│   ├── config/
│   ├── deploy/
│   ├── events/
│   ├── gitsrv/
│   ├── logs/
│   ├── proxy/             # gerador de config do Caddy
│   ├── reconcile/
│   ├── runit/             # ServiceManager (real + fake)
│   ├── store/             # SQLite + migrations/
│   ├── system/            # DeviceProbe (real + fake)
│   ├── tunnel/
│   └── web/               # handlers do painel
├── web/
│   ├── templates/
│   └── static/            # css, js, htmx
├── migrations/            # 0001_init.sql ...
├── scripts/
│   ├── poc/               # scripts da Fase 1
│   └── install.sh
├── test/
│   ├── e2e/               # roteiros para rodar no aparelho
│   └── fixtures/          # apps de exemplo (node, python, go, static)
├── docs/
│   └── tsd.md
├── Makefile
└── go.mod
```

### 15.3 Fronteiras para testabilidade (RNF-13)

Toda dependência do ambiente atrás de interface, com implementação **real** (Termux) e **fake** (testes no PC): `ServiceManager`, `DeviceProbe`, `ProxyClient` (Admin API do Caddy), `Clock`, `CommandRunner`, `GitRunner`.

---

## 16. Plano por fases

> As estimativas assumem **um desenvolvedor em meio período** e são **aproximadas**, sujeitas aos resultados da Fase 1.

### 16.1 Fase 1 — Prova de Conceito (≈ 1–3 dias)

**Objetivo:** validar as hipóteses `V-01…V-15`, medir o aparelho real e decidir **go/no-go** antes de escrever o `droidd`.

**Atividades**

| # | Atividade | Saída |
|---|---|---|
| 1.1 | Preparar o Termux (F-Droid/GitHub), Termux:API e Termux:Boot; instalar pacotes base | Ambiente funcional |
| 1.2 | Desativar o *phantom process killer* via adb sem fio; configurar isenções de bateria | Procedimento documentado e funcionando (V-08) |
| 1.3 | Subir um app Node simples sob runit com `termux-services` | Serviço supervisionado (V-02, V-09) |
| 1.4 | Subir Caddy com config JSON via Admin API | Proxy funcional (V-04) |
| 1.5 | Configurar `cloudflared` (modo token) e testar o wildcard | Acesso público funcionando (V-05) |
| 1.6 | Testar `git push` via SSH com `command=` forçado e hook | Deploy manual mínimo (V-06) |
| 1.7 | Verificar `git http-backend`, socket Unix, Go e SQLite | V-03, V-07, V-12, V-13, V-14 |
| 1.8 | Testar build real de um app Node e um Python típicos | Consumo de RAM/tempo (V-11) |
| 1.9 | Rodar o script de medição por **24 h** (Apêndice A.8) com o app sob carga leve | CSV de métricas |
| 1.10 | Verificar Termux:Boot e retomada após reboot | V-15, RNF-07 |
| 1.11 | Redigir o relatório da Fase 1 | `docs/poc-report.md` |

**Critérios go/no-go (propostos)**

| Critério | Meta |
|---|---|
| Processos vivos após 24 h com tela apagada | 100 %, sem "signal 9" |
| Temperatura máxima da bateria (uso leve) | Abaixo de um limite definido pelo usuário (sugestão inicial: < 42 °C) |
| RAM disponível mínima durante builds | Sem *OOM kill* |
| Retomada após reboot | Serviços sobem sozinhos |
| Acesso público via túnel | Funcional |

**Se algum item crítico falhar:** registrar a causa e ajustar o design (ex.: V-02 falhando → `runsvdir` dedicado; V-06/V-07 falhando → alternativa de deploy). O relatório lista **decisões alteradas** em relação a este TSD.

**Definição de pronto:** relatório publicado, todas as hipóteses marcadas (confirmada/refutada/ajustada), TSD atualizado para a v0.2.

> **Status (2026-10-12):** Fase 1 concluída com parecer **GO condicional**; ver `docs/fase-1/poc-report.md`.

### 16.2 Fase 2 — MVP sem painel (≈ 2–3 semanas)

> **Status (2026-10-16):** Fase 2 **concluída**: sub-fases 2A–2I verificadas no M22 (154 checagens automatizadas em `test/e2e/` + instalação real), AT-11 (reboot) ok e soak de 72 h com 0 mortes não intencionais (RSS do `droidd` 17 MB, CPU 0,68 %, bateria ≤ 37,9 °C). Pendente, não bloqueante: RF-16 de ponta a ponta com domínio. Ver `docs/fase-2/README.md`.

**Entregáveis**

| # | Entrega | Requisitos |
|---|---|---|
| 2.1 | Esqueleto: config, logging, SQLite + migrações, `droidd init`/`serve` | RF-24, RNF-01/02 |
| 2.2 | `ServiceManager` real + fake; criação/remoção de serviços de app | RF-03, RF-19 |
| 2.3 | Modelo de apps, portas, envdir, variáveis cifradas | RF-01, RF-02, RF-12 |
| 2.4 | Git: repositórios bare, hook, `droid hook`, `git-shell`, `authorized_keys` gerenciado | RF-04 |
| 2.5 | Pipeline de build + fila + detectores (node, static primeiro; depois python/go/custom) | RF-06, RF-07, RF-08, RF-18 |
| 2.6 | Troca de release, health check, rollback automático e manual, retenção | RF-09, RF-10, RF-11 |
| 2.7 | Proxy Caddy: gerador de config + aplicação + páginas 404/503 | RF-13, RF-14, RF-15 |
| 2.8 | Reconciliador e detecção de crash loop | RF-19, RF-20 |
| 2.9 | Logs de runtime (tail/follow) | RF-17 |
| 2.10 | API REST + tokens + eventos | RF-21, RF-22 |
| 2.11 | CLI `droid` | RF-23 |
| 2.12 | Túnel (modo token) | RF-16 |
| 2.13 | Git HTTP (se V-07 ok) e `droid backup` | RF-05, RF-25 |
| 2.14 | Documentação de instalação e operação | — |

**Ordem sugerida (fatias verticais):** 2.1 → 2.2 → 2.3 → 2.7 → 2.5/2.6 (node+static) → 2.4 → 2.8 → 2.9/2.10/2.11 → runtimes restantes → 2.12/2.13.

**Definição de pronto da Fase 2**

- Todos os RF M da Fase 2 implementados; testes unitários e de integração (com *fakes*) passando.
- Roteiro e2e no aparelho executado com sucesso (AT-01…AT-12, §17.4).
- `droidd` ocioso dentro das metas (RNF-01/02) medidas no aparelho.
- Soak test de 72 h sem mortes não intencionais de processos (RNF-10).
- Documentação mínima publicada; TSD atualizado.

### 16.3 Fase 3 — Painel web (≈ 2–3 semanas)

**Entregáveis**

| # | Entrega | Requisitos |
|---|---|---|
| 3.1 | Infraestrutura do painel: templates, layout, assets embutidos, htmx, tema | RF-40, RNF-14 |
| 3.2 | Autenticação por sessão, CSRF, cabeçalhos de segurança, troca de senha | RF-30, RF-41 |
| 3.3 | Dashboard + widget do dispositivo (somente leitura) | RF-31, RF-39 |
| 3.4 | Assistente de criação de app | RF-32 |
| 3.5 | Página do app: Visão geral, Deploys (+ ações), Ambiente, Domínios, Configurações | RF-33, RF-35 |
| 3.6 | Logs ao vivo (build e runtime) via SSE | RF-34 |
| 3.7 | Configurações globais, chaves SSH, tokens, túnel | RF-36, RF-37 |
| 3.8 | Eventos e estatísticas por app (RSS/CPU) | RF-38, RF-42 |
| 3.9 | Revisão de acessibilidade, desempenho e segurança | RNF-04, RNF-11 |

**Definição de pronto da Fase 3**

- Todos os RF M da Fase 3 implementados; testes de handlers (`httptest`) e de segurança (CSRF, autenticação, escape de logs) passando.
- Roteiro de aceitação do painel (AT-20…AT-30) executado no M22 **e** em um desktop.
- Página do dashboard dentro da meta RNF-04 na rede local.
- Nenhum recurso de rede externa carregado pelo painel (verificado por inspeção/CSP).
- Documentação do usuário (guia rápido do painel) publicada; TSD atualizado.

---

## 17. Estratégia de testes

### 17.1 Pirâmide

| Nível | Foco | Ferramentas |
|---|---|---|
| Unitário | Detectores, validações, geração de scripts `run`, geração de config do Caddy (*golden files*), cripto, máquina de estados do deploy, parsing de `droidpaas.toml`/`Procfile` | `go test` |
| Integração (PC) | `droidd` com `ServiceManager`/`DeviceProbe`/`ProxyClient` falsos; SQLite temporário; API ponta a ponta com `httptest` | `go test` |
| Integração (aparelho) | Runit real, Caddy real, git real | Scripts `test/e2e/*.sh` |
| Aceitação | Cenários de usuário (§17.4) | Roteiro manual + scripts |
| Soak/estabilidade | 72 h com apps de exemplo e métricas | Script de medição + relatório |
| Segurança | CSRF, autenticação, injeção, *path traversal*, XSS em logs | Testes automatizados + checklist manual |

### 17.2 Fixtures

Apps de exemplo em `test/fixtures/`: Node (HTTP simples com `/health`), Node com build (`npm run build`), Python (servidor simples + `Procfile`), Go, estático, e um app **defeituoso** (falha ao iniciar / não abre porta / cai em loop) para testar rollback e crash loop.

### 17.3 Casos de borda a cobrir

- Push com branch diferente do configurado; push simultâneo de dois apps (fila).
- `droidd` reiniciado no meio de um build (deploy → `interrupted`).
- Porta já ocupada; nome de app inválido/reservado; hostname duplicado.
- Caddy indisponível ao aplicar config; reaplicação após ele voltar.
- Disco cheio durante o build; release parcial não pode virar `current`.
- Logs com caracteres de controle, HTML e linhas muito longas.
- Variáveis com espaços, aspas, quebras de linha e `=`.
- Reboot com apps em estados `running`, `stopped` e `crashed` (cada um deve ser retomado conforme o desejado).

### 17.4 Testes de aceitação (resumo)

| ID | Cenário | Fase | Resultado esperado |
|---|---|---|---|
| AT-01 | `droidd init` em ambiente limpo | 2 | Estrutura, banco, admin e token criados |
| AT-02 | Criar app e ver URL do remote Git | 2 | Porta alocada, domínio e remote exibidos |
| AT-03 | `git push` de app Node simples | 2 | Build visível no terminal; app responde pelo hostname |
| AT-04 | Push com erro de build | 2 | Deploy `failed`; app anterior segue no ar (RNF-09) |
| AT-05 | Release que sobe mas falha no health | 2 | Rollback automático; status `rolled_back` |
| AT-06 | Rollback manual | 2 | Release anterior ativa e saudável |
| AT-07 | Editar variável e reiniciar | 2 | Novo valor visível ao app; segredo mascarado na API |
| AT-08 | Matar o processo do app (`kill -9`) | 2 | runit reinicia em ≤ 5 s (RNF-06) |
| AT-09 | App em crash loop | 2 | Estado `crashed`, evento com logs, sem reinício infinito |
| AT-10 | Reiniciar `droidd` (`sv restart`) | 2 | Apps e proxy seguem no ar (RNF-08) |
| AT-11 | Reboot do aparelho | 2 | Tudo retorna conforme o estado desejado (RNF-07) |
| AT-12 | Site estático servido pelo Caddy | 2 | Conteúdo servido sem processo |
| AT-20 | Login/logout e expiração de sessão | 3 | Conforme política; rate limit após falhas |
| AT-21 | Requisição POST sem token CSRF | 3 | Rejeitada (403) |
| AT-22 | Criar app pelo assistente | 3 | App criado; instruções de git corretas |
| AT-23 | Acompanhar deploy ao vivo | 3 | Logs em tempo real via SSE; cancelamento funciona |
| AT-24 | Editar variáveis pela UI e "Salvar e reiniciar" | 3 | Efeito igual a AT-07 |
| AT-25 | Logs de runtime ao vivo | 3 | Linhas aparecem em ≤ ~1–2 s |
| AT-26 | Rollback pela UI | 3 | Igual a AT-06 |
| AT-27 | Widget do dispositivo com Termux:API ausente | 3 | Mostra "indisponível" sem quebrar a página |
| AT-28 | Log com HTML/ANSI malicioso | 3 | Exibido como texto; sem execução de script |
| AT-29 | Uso em tela de 360 px | 3 | Todas as ações acessíveis sem rolagem horizontal |
| AT-30 | Remoção de app com confirmação | 3 | Serviço, rotas e dados removidos |

### 17.5 Soak test (RNF-10)

- 72 h com 3–5 apps de exemplo, requisições periódicas, um deploy a cada algumas horas e reinício aleatório de app.
- Coleta contínua: temperatura, % bateria, RAM disponível, RSS do `droidd`, número de reinícios de cada serviço, latência do health check.
- **Critério:** nenhuma morte não intencional de processo; RSS do `droidd` estável (sem vazamento visível); disco crescendo de forma previsível.

---

## 18. Riscos e mitigações

| ID | Risco | Prob. | Impacto | Mitigação |
|---|---|---|---|---|
| R-01 | Android/One UI mata processos em segundo plano (phantom killer, otimização de bateria) | Alta | Alto | Fase 1 valida (V-08); checklist de operação; wake-lock; detectar mortes e registrar |
| R-02 | Dependências nativas de npm/pip não funcionam na Bionic | Alta | Médio | Mensagens de erro claras; documentar limitações; fallback futuro via proot-distro por app |
| R-03 | RAM insuficiente durante builds (4 GB) | Média | Alto | Fila de 1 build, `nice`, flags de memória do runtime, cache compartilhado; falha com mensagem clara |
| R-04 | Calor/bateria em uso 24/7 | Média | Alto | Métricas visíveis desde a Fase 3; política ativa na Fase 4; recomendações físicas |
| R-05 | `termux-services`/runit se comportar diferente do esperado | Média | Médio | V-02/V-09; ADR-03 prevê `runsvdir` dedicado |
| R-06 | Falha do modo wildcard do túnel (V-05) | Média | Médio | Fallback para ingress por app com `config.yml` local |
| R-07 | Termux:API indisponível/instável (sensores) | Média | Baixo-médio | `DeviceProbe` tolerante a falhas; UI degrada para "indisponível" |
| R-08 | Mudanças em versões do Termux/Android quebram o ambiente | Média | Médio | Documentar versões testadas; testes e2e reexecutáveis |
| R-09 | Exposição acidental do painel/SSH à internet | Baixa | Alto | Bind local por padrão; avisos na UI; documentação de Cloudflare Access/Tailscale |
| R-10 | Corrupção do SQLite por kill abrupto/energia | Baixa | Alto | WAL, backup antes de migrar, `droid backup`, escrita atômica de arquivos |
| R-11 | Escopo crescer (Compose, Docker, bancos) | Média | Médio | Fora de escopo explícito (§1.5/§19); entregar em fases |
| R-12 | Falta de isolamento entre apps | Certa | Médio | Aceito e documentado; só código confiável |
| R-13 | Binário Go com problemas de DNS/SSL no Android (C-07) | Média | Médio | Compilar no Termux (V-13); resolvedor explícito se necessário |

---

## 19. Fora de escopo e evolução futura

| Fase | Tema | Observação |
|---|---|---|
| **4** | Governador térmico/bateria ativo | Políticas (Normal/Quente/Crítico) sobre a interface `DeviceProbe`; pausa de builds; parada de apps não essenciais; notificações |
| **4** | Escala para zero | Parar apps ociosos e acordá-los na primeira requisição (via Caddy + `droidd`) |
| **5** | Bancos gerenciados | Postgres/Redis/MariaDB como pacotes nativos sob runit, com backups |
| **5** | Webhook do GitHub (auto-deploy) | |
| **5** | Backend Docker opcional | Via VM em aparelhos que suportem (ex.: Pixel) |
| **5+** | Runtime WASM, Compose parcial, multi-aparelho | |

**Ganchos já previstos neste design:** `DeviceProbe`, `Notifier`, campos `nice` e `desired_state`, e o proxy desacoplado do runtime (que facilita a escala para zero).

---

## 20. Questões em aberto

| ID | Questão | Quando decidir |
|---|---|---|
| Q-01 | Nome final do projeto e do binário (`droid` pode colidir com outros comandos) | **Decidido (v0.3):** manter `droid`/`droidd` (nenhum binário com esses nomes no `usr/bin` do M22) |
| Q-02 | Dado o resultado de V-02, manter o `SVDIR` do `termux-services` ou usar `runsvdir` dedicado? | **Decidido (v0.2): manter o `SVDIR` do `termux-services`** (detecção ≤ 3 s, boot OK; V-02/V-15) |
| Q-03 | Como o `cloudflared` deve receber o token sem expô-lo em `ps` (variável de ambiente, arquivo)? | **Decidido (v0.2):** `TUNNEL_TOKEN` via envdir ou `--token-file` `0600`; nunca `--token` |
| Q-04 | Auto-criar app no primeiro `git push` (estilo Dokku) ou exigir criação prévia? | **Decidido (v0.3):** exigir criar o app antes do primeiro push (`git.auto_create_apps` fica `false`; push em app inexistente é negado) |
| Q-05 | Dar suporte a `pnpm`/`yarn`/`bun` na Fase 2 ou só `npm`? | **Decidido (v0.3):** só `npm` (`npm ci` com `package-lock.json`, senão `npm install` com aviso) |
| Q-06 | Convivência com mais de um usuário/admin (hoje: um) | Pós-Fase 3 |
| Q-07 | Política de versionamento do Node/Python (pacotes do Termux oferecem uma versão por vez) | **Decidido (v0.3):** usa a versão que o pacote do Termux oferece; sem gerenciador de versões |
| Q-08 | Licença do projeto (se for open source) | **Decidido (v0.3):** MIT — o repositório já traz o arquivo `LICENSE` (MIT, © 2026 Fábio de Jesus) desde o commit inicial |
| Q-09 | Limite de temperatura aceitável no M22 para uso contínuo | **Parcial (v0.2):** pico de 41,7 °C em 68 h; usar 42 °C como alerta; limite de parada fica para a Fase 4 |
| Q-10 | Estratégia de atualização do próprio `droidd` (manual vs. comando `droid self-update`) | **Decidido (v0.3):** atualização manual (`git pull` + `go build` + `sv restart droidd`, ver `docs/operacao.md`); sem `droid self-update` |

---

# Apêndice A — Templates e scripts

> Os trechos abaixo são **ilustrativos**. Caminhos e flags devem ser conferidos no aparelho durante a Fase 1.

### A.1 `run` do serviço de um app

```sh
#!/data/data/com.termux/files/usr/bin/sh
exec 2>&1
export PATH=/data/data/com.termux/files/usr/bin:$PATH
APP=/data/data/com.termux/files/home/droidpaas/apps/minha-api
cd "$APP/current" || exit 1
exec chpst -e "$APP/env" -n 10 sh "$APP/start.sh"
```

`start.sh` (gerado, `0700`):

```sh
#!/data/data/com.termux/files/usr/bin/sh
exec npm start
```

### A.2 `log/run` do serviço

```sh
#!/data/data/com.termux/files/usr/bin/sh
exec svlogd -tt /data/data/com.termux/files/home/droidpaas/logs/apps/minha-api
```

Arquivo `config` do svlogd (no diretório de logs do app):

```
s1000000
n5
```

### A.3 `run` do `droidd`

```sh
#!/data/data/com.termux/files/usr/bin/sh
exec 2>&1
export PATH=/data/data/com.termux/files/usr/bin:$PATH
export GOMEMLIMIT=64MiB
exec /data/data/com.termux/files/home/droidpaas/bin/droidd serve \
  --config /data/data/com.termux/files/home/droidpaas/etc/droidd.toml
```

### A.4 Hook `post-receive` (instalado em cada `repo.git/hooks/`)

```sh
#!/bin/sh
# Gerado pelo DroidPaaS — não edite.
exec '/abs/droid' hook post-receive --app '__APP__' --socket '/abs/run/droidd.sock'
```

(O `droid hook` lê as linhas `old new ref` do stdin, filtra o branch e conecta ao socket Unix.)

### A.5 Bootstrap do Caddy (`proxy/bootstrap.json`)

```json
{
  "admin": { "listen": "127.0.0.1:12019" },
  "apps": {
    "http": {
      "servers": {
        "dp": {
          "listen": ["127.0.0.1:18080"],
          "automatic_https": { "disable": true },
          "routes": [
            {
              "handle": [
                { "handler": "static_response", "status_code": "404",
                  "body": "DroidPaaS: nenhum app para este host." }
              ]
            }
          ]
        }
      }
    }
  }
}
```

Exemplo de rota gerada para um app de processo (dentro de `routes`):

```json
{
  "match": [{ "host": ["minha-api.apps.exemplo.com"] }],
  "handle": [
    { "handler": "reverse_proxy", "upstreams": [{ "dial": "127.0.0.1:20001" }] }
  ],
  "terminal": true
}
```

Exemplo para site estático:

```json
{
  "match": [{ "host": ["blog.apps.exemplo.com"] }],
  "handle": [
    { "handler": "file_server",
      "root": "/data/data/com.termux/files/home/droidpaas/apps/blog/current/dist" }
  ],
  "terminal": true
}
```

### A.6 Linha gerenciada em `authorized_keys`

```
# BEGIN DROIDPAAS
command="droid git-shell --key 7",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAAC3... laptop
# END DROIDPAAS
```

### A.7 Script do Termux:Boot (`~/.termux/boot/droidpaas`)

```sh
#!/data/data/com.termux/files/usr/bin/sh
termux-wake-lock
. /data/data/com.termux/files/usr/etc/profile.d/start-services.sh
```

### A.8 Script de medição da Fase 1 (`scripts/poc/measure.sh`)

```sh
#!/data/data/com.termux/files/usr/bin/sh
# Requer: pkg install jq termux-api (e o app Termux:API instalado)
OUT="${1:-$HOME/poc-metrics.csv}"
SVC="${2:-poc-app}"          # nome do serviço runit a observar
INTERVAL="${3:-60}"

[ -f "$OUT" ] || echo "ts,batt_pct,batt_temp_c,plugged,mem_avail_kb,load1,app_rss_kb" > "$OUT"

while :; do
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  b=$(timeout 5 termux-battery-status 2>/dev/null)
  pct=$(printf '%s' "$b"  | jq -r '.percentage // ""')
  tmp=$(printf '%s' "$b"  | jq -r '.temperature // ""')
  plg=$(printf '%s' "$b"  | jq -r '.plugged // ""')
  mem=$(awk '/MemAvailable/ {print $2}' /proc/meminfo)
  load=$(cut -d' ' -f1 /proc/loadavg)
  pid=$(cat "$PREFIX/var/service/$SVC/supervise/pid" 2>/dev/null)
  rss=""
  [ -n "$pid" ] && rss=$(awk '/VmRSS/ {print $2}' "/proc/$pid/status" 2>/dev/null)
  echo "$ts,$pct,$tmp,$plg,$mem,$load,$rss" >> "$OUT"
  sleep "$INTERVAL"
done
```

> Observação: `/proc/loadavg` e `supervise/pid` podem ter restrições ou formatos diferentes no aparelho; ajustar conforme V-10.

### A.9 Passos manuais da Fase 1 (resumo)

1. `pkg update && pkg install git openssh runit termux-services termux-api jq caddy cloudflared golang nodejs python`
   *(confirmar quais pacotes existem; registrar os que faltarem)*.
2. Instalar Termux:API e Termux:Boot; conceder permissões; isentar da otimização de bateria.
3. Habilitar depuração sem fio no aparelho, parear com `adb` pelo próprio Termux (`pkg install android-tools`) e **desativar o monitoramento de processos fantasmas** conforme o procedimento da versão do Android do aparelho (registrar o comando exato que funcionou).
4. Criar o app de teste (Node com `/health`), o serviço runit (`run`, `log/run`), habilitar e conferir `sv status`.
5. Subir o Caddy com o `bootstrap.json`, adicionar a rota do app via `POST /load`.
6. Configurar o túnel (token) e o hostname wildcard; testar acesso externo.
7. Configurar `authorized_keys` com `command=` e testar `git push` com hook mínimo.
8. Executar o `measure.sh` por 24 h; reiniciar o aparelho uma vez para testar o boot.
9. Preencher o relatório (hipótese → resultado → decisão).

---

*Fim do documento — TSD v0.1 (rascunho).*
