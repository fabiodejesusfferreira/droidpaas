# DroidPaaS — instalação e operação (Fases 2 e 3)

Guia para instalar o DroidPaaS num aparelho Android com Termux e usá-lo pela CLI `droid` e pela API. O painel web (§3.1) é opcional: tudo o que ele faz também sai pela CLI.

> **Aviso:** todos os apps rodam sob o mesmo usuário do Termux, **sem isolamento entre eles**. Hospede só código em que você confia.

## 1. Requisitos

- Android 11+ com **Termux** instalado pelo F-Droid ou GitHub (não pela Play Store). Termux:API e Termux:Boot são opcionais, mas recomendados.
- Pacotes: `git openssh runit termux-services caddy golang` (obrigatórios) e, conforme o uso, `nodejs python cloudflared termux-api jq`.
- O aparelho na tomada, em local ventilado, com Wi-Fi. Veja o checklist do §7.

## 2. Instalação

No Termux, com o código do projeto no aparelho:

```bash
cd droidpaas
sh scripts/install.sh
```

O script confere os pacotes (pergunta antes de instalar), compila `droidd` e `droid` **no aparelho** (binários feitos para Linux genérico podem falhar em DNS/TLS no Android), e roda `droidd init`, que:

1. cria `~/droidpaas/{bin,etc,data,run,apps,logs,cache,proxy,tmp}` com permissões `0700`;
2. gera `etc/droidd.toml`, `etc/secrets/master.key` (32 bytes, `0600`) e o banco SQLite;
3. pede o usuário e a senha do admin (mínimo 12 caracteres) e mostra o **primeiro token de API, uma única vez**;
4. cria o serviço runit `droidd` (se o `termux-services` estiver ativo) e o script `~/.termux/boot/droidpaas` — **sem alterar** scripts de boot que você já tenha.

Depois:

```bash
droid login --url http://127.0.0.1:8090 --token dp_...
droid status
```

Se o `init` avisar que o serviço não foi criado, reabra o Termux (o `runsvdir` do `termux-services` sobe junto com a sessão) e rode `sv-enable`/`droidd init` de novo.

### Portas

| Serviço | Padrão | Configuração |
|---|---|---|
| API/painel | `127.0.0.1:8090` | `http.panel_addr` |
| Caddy (entrada do túnel) | `127.0.0.1:18080` | `proxy.listen` |
| Admin API do Caddy | `127.0.0.1:12019` | `proxy.admin` |
| Apps | `127.0.0.1:20000–29999` | `ports.range_start/end` |
| sshd (git push) | `8022` | `git.ssh_port` (só informa; a porta é do sshd) |

As portas do proxy fogem de propósito de `8080`/`2019`, que costumam estar ocupadas por outros Caddy/apps do aparelho. Tudo escuta só em `127.0.0.1`; a exposição pública é pelo túnel.

## 3. Uso diário

```bash
# criar o app e conectar o git
droid apps create minha-api
git remote add droid ssh://u0_aXXX@<ip-do-celular>:8022/minha-api.git   # a URL sai do `apps create`
droid keys add ~/.ssh/id_ed25519.pub        # cadastra a chave que vai dar push
git push droid main                          # build e deploy aparecem como `remote: …`

# operar
droid apps                    # lista e estado
droid logs minha-api -f       # logs de runtime ao vivo
droid deploys minha-api       # histórico
droid deploy minha-api --rebuild
droid rollback minha-api      # volta para a release anterior
droid env minha-api set DATABASE_URL=postgres://… --secret --restart
droid domains minha-api add api.exemplo.com
droid stop|start|restart minha-api
droid apps rm minha-api --yes
```

Também dá para dar push por HTTP (sem SSH): `git remote add droid http://u:<token-git>@<painel>/git/minha-api.git`, com um token criado por `droid tokens create ci --scope git`.

### 3.1 Painel web

Abra `http://127.0.0.1:8090` no aparelho (ou o hostname do painel, veja §4) e entre com o usuário/senha do admin criados no `droidd init`. Sessão: 12 h sem uso, 7 dias no máximo; cinco senhas erradas seguidas bloqueiam o login por alguns minutos.

- **Início:** apps com estado, RAM/CPU, e o card do aparelho (bateria, temperatura, RAM, disco; exige `termux-api`, senão aparece "indisponível").
- **Novo app:** assistente em 3 passos (nome, domínio, variáveis) que mostra a URL do `git remote` para copiar.
- **Página do app:** visão geral, deploys (log ao vivo, cancelar, rollback), variáveis (segredos mascarados; "Salvar e reiniciar"), logs de runtime ao vivo (filtro, pausa, download), domínios e configurações (apagar exige digitar o nome).
- **Configurações:** domínio base, hostname do painel, retenção, token do túnel, chaves SSH, tokens de API, troca de senha e eventos.
- O painel não carrega nada de fora (sem CDN) e funciona em telas de 360 px, nos temas claro e escuro. Atrás do túnel, proteja-o também com Cloudflare Access.

### O que o deploy detecta

`droidpaas.toml` (opcional, na raiz do repositório) > `Procfile` > convenções:

| Evidência | Runtime | Build | Start |
|---|---|---|---|
| `package.json` | node | `npm ci` (ou `npm install`), `npm run build` se existir, `npm prune --omit=dev` | script `start` (vira `node arquivo` quando é simples) |
| `requirements.txt` / `pyproject.toml` | python | `venv` + `pip install` | **obrigatório** (`Procfile` `web:`) |
| `go.mod` | go | `go build -o app` | `./app` |
| `index.html` | static | opcional | — (o Caddy serve) |
| `Procfile` com `web:` | custom | opcional | o comando do `Procfile` |

O app deve escutar em `$HOST:$PORT` (`127.0.0.1` e a porta alocada). Variáveis reservadas, injetadas pelo DroidPaaS: `PORT`, `HOST`, `NODE_ENV` (node), `DP_APP`, `DP_RELEASE`, `DP_DATA_DIR` (diretório persistente do app).

Se `health_path` estiver definido (`droid` → configurações do app / `droidpaas.toml`), o deploy só é ativado quando `GET` nele responder 2xx/3xx; senão basta a porta abrir. Falhou? Rollback automático para a release anterior (status `rolled_back`). Um build que falha **nunca** derruba o que está no ar.

Limitações conhecidas: dependências nativas de npm/pip sem binário para Android costumam falhar ou demorar muito (compilam no aparelho); o `nice` não tem efeito no Android; um app composto (`npm run a && node b`) iniciado pelo `npm` pode deixar processos filhos se o `npm` morrer por SIGKILL.

## 4. Túnel e domínios (Cloudflare)

O DroidPaaS sobe o `cloudflared` em **modo token**; você cria o túnel no painel da Cloudflare (validado com `dejesusdevts.com`).

1. Zero Trust (`one.dash.cloudflare.com`) → Networks → Tunnels → Create a tunnel → Cloudflared. Copie só o **token** (a parte `eyJ…` do comando mostrado) e **não** rode os comandos da tela.
2. No aparelho: `pkg install cloudflared` (se faltar) e `droid tunnel set --token <token>` (o token é guardado cifrado e entregue ao `cloudflared` por arquivo `0600`, nunca na linha de comando). `droid status` deve mostrar o túnel `connected`.
3. De volta ao túnel → Routes → Add route → **Published application**, uma por hostname: subdomain (`painel`, `meu-app`…), domain, service `HTTP`, URL `127.0.0.1:18080` (a porta do Caddy).
4. Proteja o painel **antes** de usá-lo: Access → Applications → Add → Self-hosted → Public DNS → hostname `painel.<domínio>` → política **Allow** só para o seu e-mail (login por código).
5. `droid settings set base_domain=<domínio> panel_hostname=painel.<domínio>`. Cada app ganha `<app>.<domínio>`.

Notas:
- **Wildcard × um hostname por app.** Se o domínio já tem outro uso (portfólio, e-mail), crie um hostname por app em vez de `*`: o wildcard também capturaria subdomínios que você ainda não cadastrou. Cada app novo exige uma rota nova no túnel.
- **Certificado.** O certificado gratuito da Cloudflare cobre só um nível (`*.dominio.com`); `app.apps.dominio.com` exigiria plano pago. Por isso use o domínio raiz como `base_domain`.
- Não crie app com o nome de um subdomínio que já existe no DNS (por exemplo `www`).
- `droid status` mostra o estado do túnel (`connected`/`connecting`/`down`). Para um domínio extra de um app: rota no túnel + `droid domains APP add HOST`.
- A API e o painel escutam só em `127.0.0.1`; o painel só é público pelo hostname protegido por Access. **Nunca** exponha a API sem autenticação extra e não exponha o `sshd` à internet. Os apps ficam públicos de propósito.

## 5. Backup e restauração

```bash
droid backup --out meu-backup.db            # cópia consistente do SQLite (0600)
droid backup --out meu-backup.tar.gz --with-key   # inclui a master.key (SENSÍVEL)
```

- O backup **não** inclui o código dos apps (estão nos repositórios `apps/<app>/repo.git`) nem os dados em `shared/`.
- Sem a `master.key` as variáveis cifradas e o token do túnel **não podem ser lidos**. Guarde o `.tar.gz` com o mesmo cuidado de uma senha.
- Restaurar: com o `droidd` parado, copie o `.db` para `~/droidpaas/data/droidpaas.db` e a chave para `etc/secrets/master.key`, depois `sv up droidd`.
- Antes de aplicar uma migração de banco o `droidd` salva automaticamente `droidpaas.db.bak-<data>`.

## 6. Atualização

```bash
cd droidpaas && git pull && go build -o ~/droidpaas/bin/ ./cmd/... && sv restart droidd
```

Os apps, o Caddy e o túnel **continuam no ar** durante o restart do `droidd` (são serviços runit independentes). Migrações de banco são aplicadas ao subir, com backup prévio.

## 7. Checklist do aparelho (24/7)

- Isente o Termux (e Termux:API/Boot) da otimização de bateria; no One UI: *Apps que nunca dormem*. Fixe o Termux nos recentes.
- Desative o **phantom process killer** (Android 12+) via adb sem fio — passo a passo em [fase-1/1A-ambiente.md](fase-1/1A-ambiente.md). No PoC (Android 13) isso manteve os serviços vivos por 68 h.
- Mantenha na tomada, ventilado, sem capa; evite 100 % de carga contínua. O PoC chegou a 41,7 °C na bateria em uso normal.
- Abra o Termux:Boot uma vez após instalar; `~/.termux/boot/droidpaas` sobe o `runsvdir` no boot. Depois de um reboot **desbloqueie o aparelho uma vez** (o Android só libera o armazenamento do app depois).

## 8. Problemas comuns

| Sintoma | Causa provável | O que fazer |
|---|---|---|
| `droid` diz "sem credenciais" | falta `droid login` | `droid login --url … --token …` |
| Deploy falha em `detect` | nenhum runtime reconhecido | adicione `package.json`/`requirements.txt`/…, um `Procfile` ou fixe o runtime |
| App `crashed` | reiniciou 5× em 60 s e foi parado | `droid logs APP`, corrija e `droid start APP` ou faça um novo deploy |
| 503 no hostname | app parado, sem release ou `crashed` | `droid apps`; faça deploy/`start` |
| 404 no hostname | domínio não cadastrado no app | `droid domains APP add HOST` |
| `Permission denied` no `git push` | chave SSH não cadastrada | `droid keys add` |
| `EADDRINUSE` | outro processo na porta do app | `droid restart APP`; confira processos órfãos |
| Túnel `down` | token inválido ou sem internet | `droid logs`-equivalente: `~/droidpaas/logs/cloudflared/current` |

Logs do próprio daemon: `~/droidpaas/logs/droidd/current`; do Caddy: `~/droidpaas/logs/caddy/current`; de cada app: `~/droidpaas/logs/apps/<app>/current`; de cada deploy: `~/droidpaas/logs/deploys/<app>/<id>.log`.
