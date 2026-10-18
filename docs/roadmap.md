# Roadmap e análise de concorrentes

> **Status:** análise de viabilidade, sem código. Escrita em 2026-10-17 a partir do relatório `droidpaas-analise-roadmap.md` (pesquisa de concorrentes e ideias), revisado contra o código deste repositório. Estimativas de esforço são minhas, em dias de trabalho de uma pessoa (**P** = 1 a 3 dias, **M** = cerca de 1 semana, **G** = 2 semanas ou mais).

## 1. Resumo: o que fazer, em que ordem

Veredito: **Fazer** (vale e é viável), **Depois** (vale, mas depende de outra coisa ou não é urgente), **Validar** (falta confirmar um risco antes de investir), **Não fazer**.

| # | Item | Veredito | Esforço | Por quê |
|---|---|---|---|---|
| 1 | [`droid doctor`](#6-droid-doctor) | Fazer | P | Tira as falhas de ambiente mais comuns; quase tudo já é lido pelo código |
| 2 | [Auto-deploy por polling](#1-auto-deploy-por-polling) | Fazer | P | Deploy contínuo sem domínio nem webhook; reaproveita o `ghsrc` |
| 3 | [Alertas e heartbeat](#2-alertas-e-heartbeat) | Fazer | M | Hoje ninguém sabe que o celular caiu |
| 4 | [2FA com TOTP](#4-2fa-com-totp) | Fazer | P | Fecha o ponto fraco do painel fora do Cloudflare Access |
| 5 | [Backup fora do aparelho, com `shared/`](#5-backup-fora-do-aparelho) | Fazer, em 3 etapas | M a G | Maior risco de perda de dados da categoria |
| 6 | [APK (fachada leve)](#7-apk) | Depois (já planejado) | M a G | Maior alavanca de adoção; depende de `doctor` e `pair` para ser leve |
| 7 | [Cron declarativo](#8-cron-declarativo) | Depois | M | Completa o "Heroku de bolso"; precisa de um agendador novo |
| 8 | [`droid mcp` somente leitura](#10-servidor-mcp) | Depois (experimento barato) | P | Diferencial barato; risco de prompt injection controlável |
| 9 | [`droid pair`](#3-pareamento-droid-pair) | Depois, junto com o APK | P a M | Reduz o onboarding, mas aumenta a superfície de autenticação |
| 10 | [Previews por branch](#9-previews-por-branch) | Validar (MVP só estático) | M a G | Diferencial real, mas pesa em RAM, fila e segurança |
| 11 | [Serviços de um clique](#11-serviços-de-um-clique-redis-e-postgres) | Validar; Redis antes de Postgres | M (Redis), G (Postgres) | Pacotes existem no Termux; falta testar queda de energia |
| 12 | [Tailscale](#12-tailscale) | Validar; só documentação por ora | P (doc) | **Não há pacote `tailscale` no Termux** (ver §2) |
| 13 | [Métricas de tráfego por app](#13-métricas-de-tráfego-por-app) | Depois, com a escala para zero (Fase 4) | M | Só vale junto com o sinal de "app ocioso" |

Não entram agora: cluster entre aparelhos, armazenamento USB/SD, terminal web, e-mail ou Telegram como armazenamento principal, apps de mídia e imagens OCI (ver [§4](#4-o-que-não-fazer)).

**Por que esta ordem difere do relatório original.** Coloquei `droid doctor` e polling na frente porque custam 1 a 3 dias e removem as duas maiores fontes de abandono (ambiente mal configurado e falta de domínio). Movi `droid pair` para junto do APK: o onboarding manual são três passos feitos uma vez, e o código de pareamento cria uma superfície de autenticação nova que só compensa quando houver um cliente (o APK) para usá-la.

## 2. O que foi verificado nesta revisão

**Confirmado no código deste repositório:**

- A tabela `events` tem `level`, `kind`, `app_id` e `data` (JSON), e os tipos já emitidos incluem `app.crash_loop`, `deploy.succeeded`, `deploy.<status>` (falha, rollback, cancelado), `proxy.apply_failed`, `tunnel.*` e `github.sync_failed`.
- `build.RepoConfig` (em `internal/build/config.go`) lê o `droidpaas.toml` em modo estrito, e o reconciliador tem um passo de limpeza de órfãos (`sweepOrphans`) que serve de molde para limpezas agendadas.
- A leitura da bateria já traz o campo `Health`; o coletor de métricas guarda 240 pontos a cada 30 s (2 h); o pacote `tunnel` só fala `cloudflared`.
- O TSD já reserva para a **Fase 4**: governador térmico e de bateria, escala para zero e o gancho `Notifier`. O backup do banco **não** inclui `shared/` (TSD §14.3).

**Corrigido em relação ao relatório original:**

- O relatório diz que a tabela `api_tokens` não tem coluna de escopo. **Ela tem**: a migração `0002` adicionou `scope` (valores atuais `full` e `git`). Para o MCP basta **acrescentar valores** (`read`, `operate`), sem coluna nova; só a lógica de autorização precisa aprender os novos escopos.
- O homedroid tem hoje **64 estrelas**, não 47 (conferido no README em 2026-10-17). Os demais dados do relatório sobre ele conferem: checagem do branch a cada 5 minutos sem webhook, a versão antiga continua se o build falha, sem rollback citado, licença GPL-3.0, serviços em Alpine sob proot, módulo Tailscale e pareamento entre aparelhos por mTLS.

**Pacotes do Termux** (índice `termux-main` para aarch64, lido em 2026-10-17):

| Pacote | Situação |
|---|---|
| `redis` | Existe (versão 8.10.2) |
| `postgresql` | Existe (versão 18.6, cerca de 30 MB instalados) |
| `cronie` | Existe |
| `tailscale` | **Não existe** nesse repositório |
| `wireguard-tools` | Existe, mas o túnel nativo precisa de interface TUN (normalmente root) |
| `ntfy` | Não é pacote; para **enviar** alertas basta um POST HTTP |

**Não verificado:** os outros cinco concorrentes (li só o relatório), o uso de qualquer concorrente em prática, `tailscaled` em modo userspace dentro do Termux, e réplicas do `cloudflared` com o mesmo token. Evite "primeiro" ou "único" em material público: a busca foi limitada.

## 3. Itens, um a um

### 1. Auto-deploy por polling

- **Veredito: fazer. Esforço: P.**
- **Valor:** quem não tem domínio, Cloudflare ou webhook público passa a ter deploy contínuo a cada push no GitHub. Hoje o webhook exige o painel acessível pela internet.
- **Encaixe no código:** `ghsrc.Service.Sync` já faz `git fetch` para o `repo.git` e enfileira o deploy, com um mutex por app. O gatilho `github` já existe.
- **Como implementar:**
  1. Migração `0006`: em `github_sources`, `poll_seconds` (0 = desligado) e `last_seen_sha`.
  2. Passo novo no reconciliador (`github-poll`): para cada app com polling, `git ls-remote <url> refs/heads/<branch>` (mesma URL validada e mesma deploy key do `fetch`; extrair a montagem do ambiente num helper comum).
  3. Se o sha difere de `last_seen_sha`, gravar o novo sha **antes** de chamar `Sync`.
  4. Aba GitHub: caixa "Verificar a cada N minutos (sem webhook)", padrão desligada; CLI `droid github APP poll 5m|off`.
- **O detalhe que decide:** guardar o último sha **tentado**, não o publicado. Sem isso, um commit que quebra o build seria reconstruído a cada ciclo, gastando bateria e a fila de 1 build.
- **Cuidados:** jitter, timeout de uns 15 s, backoff exponencial em erro de rede, intervalo mínimo de 1 minuto, e pausa quando o governador térmico (Fase 4) existir.
- **Aceite:** teste com o gancho `Remap` (sem rede): sha novo cria 1 deploy; mesmo sha não cria; build que falha não é repetido; erro de rede aparece em "Último erro".

### 2. Alertas e heartbeat

- **Veredito: fazer. Esforço: M.** É o item de maior valor de confiabilidade: o modo de falha mais provável de um servidor num celular é ele sumir.
- **Dois mecanismos diferentes:**
  - **Alertas** (o droidd avisa): crash loop, deploy falho ou com rollback, túnel caído, falha ao aplicar o proxy, disco baixo, bateria e temperatura.
  - **Heartbeat** (o droidd avisa que está vivo): um ping periódico a um serviço externo; **a ausência do ping** dispara o aviso. É a única forma de detectar que o celular travou, morreu ou perdeu a rede, o que um alerta enviado pelo próprio aparelho não cobre.
- **Como implementar:**
  1. Pacote `internal/notify` com a interface `Notifier` (o gancho já previsto no TSD) e três provedores com `net/http`: **ntfy** (um POST), **webhook genérico** (Discord, Slack, Telegram) e **`termux-notification`** local.
  2. Um assinante da tabela `events` (no `events.Log.Add`) que filtra por `kind` e `level`, com **cooldown por tipo e app** e mensagem de "resolvido".
  3. Temperatura e bateria com **histerese** (alerta a 42 °C, normaliza a 40 °C). Alerta extra se `Health` da bateria for diferente de `GOOD`: bateria velha ligada 24/7 é risco físico.
  4. Heartbeat: passo do reconciliador que faz `GET <url>` a cada N minutos (formato do healthchecks.io).
  5. Configurações → **Alertas** no painel e `droid alerts`; URLs e tokens cifrados com o `crypt.Box`.
- **Cuidados:** tópicos públicos do ntfy se protegem só pelo nome, então gerar um nome longo e aleatório (ou usar servidor próprio). **Tudo desligado por padrão e opt-in**: o heartbeat é uma requisição externa do servidor, o que deve estar escrito na documentação (o painel continua sem carregar recursos de fora).
- **Aceite:** com um servidor HTTP de teste, forçar crash loop gera 1 aviso (não 10), e a recuperação gera 1 "resolvido"; sem ping de heartbeat o serviço externo de teste marca a falha.

### 3. Pareamento (`droid pair`)

- **Veredito: depois, junto com o APK. Esforço: P a M.**
- **Valor:** reduz o onboarding (hoje `droid login`, `droid keys add` e `git remote add`) a um passo. Para quem usa só o celular, o ganho é pequeno; para o APK e para notebooks, é grande.
- **Como implementar:** uma tabela de códigos de uso único (guardados como hash, validade de 5 min, 8 caracteres de `crypto/rand`), um endpoint público `POST /api/v1/pair` com **limite por IP e por código** (poucas tentativas), e `droid pair <url> <código>` que troca o código por um token novo (nome = hostname do computador), envia a chave pública SSH e imprime o `git remote add` pronto. O código vai no corpo, nunca na URL.
- **Melhor que o backseat:** aceita vários clientes, o token tem nome e pode ser revogado no painel (a tabela já tem `last_used_at` e `revoked_at`).
- **Risco:** é uma rota pública que emite credenciais. Por isso só entra com revisão de segurança e testes de força bruta, e QR só faz sentido com o APK.

### 4. 2FA com TOTP

- **Veredito: fazer. Esforço: P.**
- **Valor:** atrás do Cloudflare Access o ganho é pequeno; por LAN ou Tailscale, o painel hoje depende só de senha, limite de tentativas e sessão.
- **Como implementar:** TOTP (RFC 6238) cabe na stdlib (`crypto/hmac` com SHA-1, algumas dezenas de linhas). Migração com `users.totp_secret` (cifrado com o `crypt.Box`), `totp_last_step` (anti-replay) e uma tabela de códigos de recuperação com hash. O login do painel ganha uma segunda etapa; **tokens da CLI e da API ficam de fora**.
- **Cuidados:** desvio de relógio do celular (aceitar ±1 janela e mostrar a hora do servidor no erro); `droidd user reset-2fa`, que só roda **localmente no aparelho**, para o dono não ficar trancado fora; compartilhar o limite de tentativas do login.
- **Aceite:** código reutilizado é recusado; código de recuperação funciona uma vez; a CLI continua funcionando com token.

### 5. Backup fora do aparelho

- **Veredito: fazer, em três etapas. Esforço: M a G.** É o principal risco de durabilidade: se o celular morrer, o código volta do Git, mas os dados persistentes dos apps (`shared/`, `DP_DATA_DIR`) se perdem.
- **Etapa 5a (M):** opção por app para incluir `shared/` (com limite de tamanho e `backup_cmd` pré-backup no `droidpaas.toml`, porque um banco aberto não é copiado de forma consistente); arquivo **cifrado com senha** (argon2id mais AEAD, ambos já disponíveis no `x/crypto` e no `crypt`), em vez de depender de `--with-key`.
- **Etapa 5b (M):** destinos **SFTP** (o `x/crypto/ssh` já é usado no `ghsrc`) e WebDAV; S3-compatível só se houver demanda.
- **Etapa 5c (M):** agenda e retenção (por exemplo 7 diários e 4 semanais), alerta se o último backup falhou ou tem mais de 48 h, e **`droid restore` com teste e2e num sandbox**: um backup que nunca foi restaurado não conta.
- **Cuidados:** nunca gravar a `master.key` junto de um destino remoto por padrão; mostrar o tamanho estimado antes; ignorar `node_modules` e caches dentro de `shared/` por configuração.

### 6. `droid doctor`

- **Veredito: fazer primeiro. Esforço: P.**
- **O que verifica**, cada falha com "como corrigir": origem do Termux (F-Droid ou GitHub, não Play Store; o comando `termux-info` informa a variante, a confirmar no aparelho), `sshd` e `runsvdir` de pé, `droidd`, Caddy e túnel saudáveis, Termux:API respondendo, script do Termux:Boot, permissões de `authorized_keys`, espaço em disco, DNS, relógio (comparando com o cabeçalho `Date` de uma resposta HTTPS) e saúde e temperatura da bateria.
- **Limite:** a isenção de bateria e o *phantom process killer* talvez não sejam legíveis sem `adb`; o que não for detectável vira instrução na saída.
- **Bônus:** `droid doctor --report` gera um texto em Markdown, **sem tokens nem senhas**, para anexar a relatos de problema.
- **Aceite:** teste com `FakeProbe` e `runit` falso cobrindo cada verificação; execução manual no aparelho antes do release.

### 7. APK

- **Veredito: depois (já planejado em [`planejamento-apk.md`](planejamento-apk.md)). Esforço: M a G.**
- **Valor:** a barreira do terminal pesa na adoção (o homedroid, que é um APK, tem mais estrelas que todos os outros concorrentes lidos). Manter a fachada **sem proot** é o diferencial: o app fica muito mais leve que um Alpine sob proot.
- **Do relatório, para acrescentar ao plano:** um **assistente de ambiente** (leva às telas do Android para isenção de bateria e confere Termux:Boot e Termux:API), `SHA256SUMS` gerados pelo CI nas releases, e o botão "Atualizar" chamando `droid update`.
- **Ordem:** depois do `droid doctor` (o app pode reaproveitar suas verificações) e do `droid pair`.

### 8. Cron declarativo

- **Veredito: depois. Esforço: M.** É o que o Heroku Scheduler resolve; nos concorrentes lidos não apareceu. Sem ele, quem precisa de tarefas agendadas usa o `crond` do Termux (pacote `cronie`), que não versiona com o código nem volta no rollback.
- **Desenho:** tarefas no `droidpaas.toml`, versionadas junto com o código:

```toml
[[cron]]
name     = "limpeza"
schedule = "0 3 * * *"
command  = "node scripts/cleanup.js"
catchup  = "once"   # celular desligado às 03:00? roda uma vez ao voltar
overlap  = "skip"
timeout  = "10m"
```

- **Como implementar:** campo `Cron []CronJob` no `build.RepoConfig` (o modo estrito já recusa chaves desconhecidas); um laço de 30 a 60 s no `droidd` que roda o comando como processo filho com o mesmo `envdir` e `nice` do app; tabela `cron_runs` (agendado, início, fim, código de saída, caminho do log); erros viram `events`. Um parser mínimo de 5 campos (`*`, listas, intervalos, passos) em vez de dependência nova, com testes tabulares.
- **Decisões a tomar antes:** `catchup` (padrão `none`), sobreposição (padrão `skip`), e se o cron obedece ao governador térmico. Filhos do agendador não devem sobreviver a um restart do `droidd`: rodá-los no mesmo grupo de processos e encerrá-los no desligamento.

### 9. Previews por branch

- **Veredito: validar; começar só com apps estáticos. Esforço: M a G.**
- **Valor:** é um diferencial real, mas a combinação de 4 GB de RAM, fila de 1 build e ausência de isolamento entre apps o torna o item mais arriscado da lista.
- **MVP seguro:** só para apps `static` (custo de RAM zero), no máximo 2 previews vivos, TTL de 7 dias, apenas branches **do próprio repositório conectado** (nunca PRs de forks, porque todo build roda com o usuário do Termux).
- **Como implementar:** o webhook de `push` já traz o `ref`; se não for o branch do app, criar um app filho `<app>-<slug>` (o nome precisa casar com `^[a-z][a-z0-9-]{1,30}$`, então truncar e acrescentar hash curto; hostname de **um único nível**, por causa do certificado gratuito da Cloudflare). Migração com `apps.parent_id`, `apps.preview_branch` e `apps.expires_at`; remoção no evento `delete` ou pelo TTL, num passo do reconciliador no molde do `sweepOrphans`.
- **Risco de dados:** o preview herdar a `DATABASE_URL` de produção. Oferecer um bloco `[previews.env]` de sobrescrita, injetar `DP_PREVIEW=1` e avisar na criação.
- **Opcional:** comentar a URL no PR exige token com escrita (a deploy key é só leitura); deixar fora da primeira versão.

### 10. Servidor MCP

- **Veredito: depois, como experimento barato e somente leitura. Esforço: P.**
- **Fase 1, local:** `droid mcp` por **stdio** (JSON-RPC, stdlib), que usa o token do `droid login`; nenhuma superfície nova exposta na rede. Ferramentas, todas sobre endpoints que já existem: listar apps, status, log de deploy, logs de runtime, eventos, saúde do sistema. **Nunca valores de segredos.**
- **Escopos:** reaproveitar a coluna `scope` existente com os valores novos `read` e `operate`; o token MCP nasce `read`. Reiniciar, rollback e disparar deploy exigem `operate`; alterar variáveis ficam fora.
- **Risco principal, prompt injection:** logs de build e de apps têm texto controlado por terceiros (saída de scripts de dependências). Mitigações: somente leitura por padrão, confirmação humana nas ferramentas destrutivas, logs tratados como dados não confiáveis e cada chamada MCP auditada em `events`.
- **Fase 2, remota** (`/mcp` no mesmo mux, com OAuth 2.1): só depois, e só com demanda real.
- **Exemplo de uso:** "meu deploy falhou, por quê?" leva o agente a ler o log e os eventos e achar um `ENOSPC`.

### 11. Serviços de um clique (Redis e Postgres)

- **Veredito: validar; Redis antes de Postgres.**
- **Viabilidade:** os dois pacotes existem no Termux (ver §2). O Postgres instala cerca de 30 MB e disputa RAM com os apps num aparelho de 4 GB.
- **Passo zero (custo zero):** documentar o **SQLite em `DP_DATA_DIR`** como caminho recomendado; o TSD trata bancos gerenciados como Fase 5+.
- **Desenho:** `droid addons add meu-app redis` cria um serviço runit `dp-addon-redis-meu-app` ligado a `127.0.0.1` numa porta do alocador existente, gera as credenciais, grava como segredo e injeta `REDIS_URL` ou `DATABASE_URL`. Dados em `addons/` dentro do diretório do app e no backup (com dump antes do tar). Padrões ajustados para 4 GB (Redis com `maxmemory` baixo; Postgres com poucas conexões).
- **Validar antes:** comportamento após `kill -9` e queda de energia (o risco R-10 do TSD já aponta corrupção), e consumo de RAM real no M22.
- **Ordem:** Redis (M), depois Postgres (G).

### 12. Tailscale

- **Veredito: validar; por ora, só documentação. Esforço: P (doc).**
- **O fato novo:** o índice do Termux não tem pacote `tailscale`. As opções são (a) o **app oficial do Tailscale para Android** com o Caddy ou o painel escutando na interface da tailnet, o que não exige código além de documentar como configurar o `panel_addr`; ou (b) compilar o `tailscaled` em modo userspace no próprio aparelho, **não testado**.
- **Valor:** acesso privado ao painel e ao `git push` sem domínio, um segundo caminho se a Cloudflare falhar e o fim do `ssh -N -L` do README.
- **Decisão:** não criar a abstração de "provedores de túnel" agora. Primeiro fazer um teste de validação no estilo V-xx da Fase 1 (a opção a funciona? a b compila e conecta?). Se a opção (a) bastar, a documentação resolve.
- **Cuidado:** expor o painel na tailnet o deixa acessível a qualquer dispositivo dela; manter login e, depois, 2FA.

### 13. Métricas de tráfego por app

- **Veredito: depois, junto com a escala para zero (Fase 4). Esforço: M.**
- **Valor:** é o sinal de "app ocioso" de que a escala para zero precisa, e os 5xx por app alimentam os alertas.
- **Desenho:** o Caddy já está na frente de todos os apps. Ativar o log de acesso JSON por rota (o pacote `proxy` gera a configuração) e agregar **em memória** em contadores por app e minuto: requisições, 2xx/4xx/5xx e latência p50/p95. Não gravar IP nem caminho completo. Visitante único seria opcional, com hash e sal diário.
- **Cuidado:** o custo de I/O do log num aparelho de 4 GB; agregar em memória e fazer rotação. Pode haver um caminho mais barato pelas métricas do próprio Caddy; **não verifiquei** se elas separam por hostname.

## 4. O que não fazer

- **Terminal web:** todos os apps rodam no mesmo usuário; um painel comprometido viraria shell completo.
- **E-mail e Telegram como armazenamento principal:** fogem do foco de PaaS e criam dependência de terceiros. (Telegram ou e-mail como *canal de alerta* é diferente e entra no item 2.)
- **Apps de mídia (Jellyfin, Immich) e imagens OCI sob proot:** pesam e quebram os menos de 20 MB de RAM que o README promete.
- **Cluster entre aparelhos:** alta disponibilidade é trabalho grande. Uma hipótese barata a testar um dia: dois aparelhos com o mesmo token de túnel do `cloudflared` como réplicas (não validado).
- **Armazenamento USB ou SD para releases:** FAT e exFAT não suportam symlink, e o swap atômico do `current` depende dele. Se um dia houver suporte, só para logs e backups.

## 5. Concorrentes: panorama resumido

Dados dos READMEs lidos em 2026-10-17. Dokku, CapRover e Coolify dependem de Docker, que não roda no Termux sem root (informação prévia, não verificada aqui).

| Projeto | Deploy | Rollback | Base técnica | Observação |
|---|---|---|---|---|
| **DroidPaaS** | `git push` (SSH/HTTP), webhook do GitHub | Sim, com health check | Binário Go, runit, Caddy | Soak de 72 h num M22, testes e2e no aparelho |
| [homedroid](https://github.com/sudiptacmd/homedroid) | URL do repositório, checagem a cada 5 min | Mantém a versão anterior se o build falha | APK Kotlin, Alpine sob proot | 64 estrelas, beta 0.9.4, GPL-3.0 (conferido) |
| [MiniShinobi](https://github.com/Mic-360/MiniShinobi) | Webhook com HMAC ou botão | Não citado | Node, PM2, Nginx | Só JavaScript (relatório original) |
| [termux-hosting-panel](https://github.com/himalayladha/termux-hosting-panel) | Upload e editor | Não | Node e SQLite | Tem TOTP e backup para o Telegram (relatório original) |
| [backseat](https://github.com/SaiNayakk/backseat) | `scp` de uma pasta | Não | Python | Painel só leitura (relatório original) |
| [mobile-home-server](https://github.com/handriss/mobile-home-server) | `scp` | Não | Scripts e nginx | Heartbeat e alertas, MCP remoto (relatório original) |
| [Dot-Tux](https://github.com/SullyGreene/Dot-Tux) | Nenhum | Não | Flask e Nginx | Só LAN (relatório original) |

**A diferença a defender:** `git push` por SSH com o log no terminal, rollback com health check, binário Go único (sem Node, PM2 nem proot), várias linguagens, freio contra crash loop, segredos cifrados, documentação e painel em português e evidência de campo publicada. Os números de campo (soak de 72 h, painel em cerca de 40 ms, menos de 20 MB de RAM) vêm das medições deste repositório (`docs/fase-2`, `docs/fase-3`).

## 6. Já planejado fora desta análise

- **GitHub, etapas 3 e 4:** "Conectar GitHub" com lista de repositórios (GitHub App) e devolução do status do deploy ao commit (ver `docs/operacao.md` §3.2).
- **Pacote oficial do Termux** (`pkg install droidpaas`), depois de haver usuários.
- **Site do projeto**, **GIF do `git push`** e **posts de divulgação**.
- **Fase 4 do TSD:** governador térmico e de bateria, escala para zero, notificações.

## 7. Como manter este documento

- Ao concluir um item, mude o veredito para **Feito** e aponte o commit ou a seção de `operacao.md` que o documenta.
- Ao validar um risco (por exemplo o Tailscale), registre o resultado na seção do item e na tabela do §2, com a data.
- Refaça a leitura dos concorrentes antes de usar qualquer número deles em material público; projetos pequenos mudam rápido.
