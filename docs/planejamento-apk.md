# Planejamento: app Android "fachada leve" (APK)

> **Status: planejamento. Nenhum código foi escrito.** Este documento descreve o que será construído, como será testado e distribuído, e o que ainda é incerto.

## 1. Objetivo

Quem não quer usar terminal deve conseguir **instalar, iniciar e abrir o painel do droidpaas tocando em botões**. O app não substitui o Termux nem o `droidd`: ele é uma **fachada** que comanda o Termux e mostra o painel numa janela própria.

Fora de escopo (por enquanto): rodar o servidor sem o Termux, empacotar o binário Go dentro do APK, notificações, widgets, gerenciar apps (isso é do painel).

## 2. O que o app faz

| Tela / ação | Comportamento |
|---|---|
| **Status** | Consulta `http://127.0.0.1:8090/healthz` a cada ~3 s (só com o app aberto) e mostra: *no ar*, *parado ou não instalado*, *Termux não encontrado*. |
| **Instalar** | Abre uma sessão **visível** no Termux que instala os pacotes e roda `scripts/install.sh` (pede usuário e senha de admin e mostra o token uma vez). É o único passo que exige o usuário digitar. |
| **Iniciar** | Em segundo plano no Termux: carrega o `runsvdir` do termux-services e faz `sv up droidd`. |
| **Reiniciar** | `sv restart droidd` (os apps hospedados seguem no ar). |
| **Abrir painel** | Tela cheia com uma WebView apontada para `http://127.0.0.1:8090`; links externos abrem no navegador. Só habilitado quando o status é *no ar*. |
| **Abrir o Termux** | Atalho para o app do Termux. |
| **Autorizar este app no Termux** | Copia o comando de configuração (§3) e pede a permissão do Android. Aparece até a permissão ser concedida. |

Texto sempre em português do Brasil, no mesmo tom do painel.

## 3. Como ele fala com o Termux

O Termux expõe a intent `RUN_COMMAND`. Ela exige **duas** autorizações do usuário, uma única vez:

1. No Termux: `allow-external-apps=true` em `~/.termux/termux.properties` (o app copia o comando; o usuário cola e reabre o Termux).
2. No Android: permissão `com.termux.permission.RUN_COMMAND` para o nosso app (diálogo em tempo de execução).

Cada ação é um comando **fixo e conhecido** (constantes no app). O app **nunca** executa texto vindo de fora (intents, links, painel).

Pontos a verificar na implementação: detectar o Termux instalado (Android 11+ exige declarar o pacote em `<queries>`); tratar a recusa quando o Termux está de versão antiga ou da Play Store (não suportada); e o que acontece se o Android impedir iniciar o serviço do Termux com o app em segundo plano (por isso os botões só funcionam com o app aberto).

## 4. Segurança

- WebView: JavaScript ligado (o painel usa htmx e SSE), **sem** acesso a arquivos, sem `addJavascriptInterface`, sem carregar nada fora de `127.0.0.1:8090`; qualquer outro endereço sai para o navegador.
- Tráfego em texto puro (HTTP) liberado **só** para `127.0.0.1` e `localhost`, via configuração de rede do app; o resto do app fica bloqueado para HTTP.
- Atividades internas não exportadas; nenhuma permissão além de `INTERNET` e `RUN_COMMAND`.
- Aviso na primeira abertura: dar a um app a permissão de rodar comandos no Termux é poderoso; só instale o APK dos canais oficiais do projeto e confira a assinatura.
- A senha do painel continua sendo do painel; o app não a guarda.

## 5. Decisões de projeto

- **Linguagem e dependências:** Java (ou Kotlin) usando só a API do Android, **sem bibliotecas externas**. Menos peso, build reproduzível mais fácil, revisão de segurança mais simples.
- **`minSdk` 30** (Android 11, igual ao DroidPaaS) e `targetSdk` atual.
- **Identificador do app:** definir antes do primeiro release (ele não muda depois). Sugestão: `dev.droidpaas.launcher`, ou um nome ligado a um domínio que você registre.
- **Nome e ícone:** "droidpaas" e o `icon-app` da pasta `assets/logo/`, conforme a [política de marca](../TRADEMARKS.md).
- **Licença:** MIT para o código do app; nome e logo continuam reservados.
- **Onde fica:** pasta `android/` neste repositório (projeto Gradle independente do módulo Go).

## 6. Distribuição

1. **GitHub Releases:** APK assinado com uma chave de lançamento **guardada fora do repositório** (nunca no Git). Publicar também o hash SHA-256.
2. **F-Droid:** exige código aberto, build a partir do código-fonte e sem bibliotecas proprietárias, o que o desenho acima respeita. Passos: tags de versão limpas (`versionCode` crescente), metadados do app (descrição, capturas, ícone) e um pedido de inclusão no repositório de metadados do F-Droid (`fdroiddata`). A revisão pode levar semanas. Pergunta aberta: usar a assinatura do F-Droid ou builds reproduzíveis com a nossa chave.
3. **Google Play:** não recomendado agora (políticas sobre apps que executam comandos em outro app; e o próprio Termux não é suportado lá).

## 7. Testes

- **Automáticos (CI):** compilação do APK; testes unitários da lógica sem Android (por exemplo, decidir se uma URL fica na WebView ou sai para o navegador; montar os comandos).
- **Manuais, em pelo menos dois aparelhos:** instalação do zero, "Iniciar" depois de reiniciar o celular, recusa da permissão, Termux ausente, painel (login, log ao vivo, rolagem, botão voltar, tema escuro) e rotação de tela.
- **Critério de aceite:** um usuário sem experiência instala o Termux e o APK, toca em *Instalar*, cria o admin, abre o painel e publica um app, sem digitar nada além do usuário/senha de admin e do comando de autorização.

## 8. Estimativa e etapas

| Etapa | Entrega | Esforço |
|---|---|---|
| 1 | Esqueleto, status e abrir painel (WebView) | 1 dia |
| 2 | Integração com o Termux (autorizar, iniciar, reiniciar, instalar) | 1–2 dias |
| 3 | Textos, ícone, tratamento de erros, tema | 1 dia |
| 4 | CI, assinatura, release no GitHub | 1 dia |
| 5 | Testes em aparelhos reais e correções | 2–3 dias |
| 6 | Metadados e pedido ao F-Droid | 1 dia (+ espera da revisão) |

Total: cerca de uma a duas semanas de trabalho, mais a espera do F-Droid.

## 9. Riscos e perguntas em aberto

- A experiência depende de **duas permissões manuais** (Termux e Android). É o maior atrito; vale testar se um passo a passo na própria tela basta.
- Mudanças do Android em serviços em segundo plano ou nas regras de WebView podem exigir ajustes.
- O Termux do F-Droid e o do GitHub têm assinaturas diferentes; confirmar que o app funciona com ambos.
- O comando de *Instalar* clona o repositório público: o fluxo muda se o repositório mudar de endereço. Alternativa: baixar uma versão marcada (tag) em vez da branch principal.
- Decidir se vale oferecer *Parar*. Parar o `droidd` não derruba os apps; o rótulo precisaria deixar isso claro.
