# droidpaas — Design System

Fonte de verdade dos tokens: o bloco de variáveis no topo de [`web/static/app.css`](../web/static/app.css) (um CSS único, escrito à mão, sem build: TSD §12.5). Este documento explica o porquê e as regras de uso. Se os dois divergirem, vale o CSS e este arquivo está desatualizado.

Escopo: painel web responsivo, mobile-first (celular é o uso principal; desktop é o segundo). Tablet não tem layout próprio: usa o do celular até 899 px. Ponto de partida: prints de um painel de PaaS de código aberto, reformulado (seção 11).

## 1. Princípios

1. **Cor só para estado e ação.** Neutros quentes por padrão; verde/âmbar/vermelho dizem o que está acontecendo; o azul da marca diz o que é clicável.
2. **Densidade técnica, alvo de toque honesto.** Compacto com mouse em tela larga, 44 px com toque. Nunca as duas coisas ao mesmo tempo.
3. **Borda em vez de sombra.** Hierarquia por superfície e contorno; nada flutua.
4. **Legível ao sol e em conexão ruim.** Contraste AA no mínimo, zero fontes ou ícones remotos, nada que dependa de rede para renderizar.
5. **O estado vem antes do texto.** Quem olha o card deve ver "no ar" ou "caiu" pela régua colorida antes de ler.

## 2. Cores

Dois temas (`claro`, `escuro`), mais `auto` que segue o sistema. O escuro é o tema de referência (os prints são escuros); o claro é papel quente, não branco puro.

### Paleta base

| Token | Claro | Escuro | Papel |
|---|---|---|---|
| `--bg` | `#f5f3ef` | `#0e0d0c` | Fundo da página |
| `--surface` | `#fffefb` | `#161513` | Cards, painéis, sidebar, campos |
| `--code-bg` | `#ece8e1` | `#211f1c` | Código inline, item ativo do menu, campo somente leitura, badges, hover de superfície |
| `--text` | `#1a1815` | `#f2efe9` | Texto primário |
| `--muted` | `#5c564e` | `#a39d93` | Texto secundário, rótulos |
| `--border` | `#d8d2c8` | `#2d2a26` | Divisores e contorno decorativo |
| `--border-strong` | `#857e73` | `#7a746a` | Contorno de campos e botões (componente de UI, ≥ 3:1) |
| `--primary` | `#0b5fff` | `#5b8cff` | Ação principal, links, foco, item ativo (azul da logo) |
| `--primary-text` | `#ffffff` | `#0b1220` | Texto sobre `--primary` |
| `--ok` | `#17722f` | `#4cc26a` | Rodando, concluído, sucesso |
| `--warn` | `#8a5a00` | `#e0b04a` | Iniciando, em andamento, aviso |
| `--danger` | `#b3261e` | `#ff6b61` | Erro, crash loop, ação destrutiva |
| `--focus` | `#0b5fff` | `#8fb0ff` | Anel de foco (3 px) |
| `--log-bg` / `--log-text` | `#14120f` / `#e4e0d8` | `#090807` / `#e4e0d8` | Console de logs (escuro nos dois temas) |

Não existe token `--info`: informação neutra usa `--text`/`--muted`, e o azul fica reservado para ação.

### Escala do acento (proposta, só 400 e 600 estão no CSS)

| 50 | 100 | 200 | 300 | **400** | 500 | **600** | 700 | 800 | 900 |
|---|---|---|---|---|---|---|---|---|---|
| `#ecf2fe` | `#d8e5fd` | `#adc9ff` | `#7aa8ff` | `#5b8cff` | `#2973ff` | `#0b5fff` | `#004ad6` | `#0038a3` | `#002770` |

400 é o acento do escuro e 600 o do claro, ambos oficiais da logo. Os demais são sugestões para quando houver necessidade (hover, fundo tingido); não os adicione ao CSS antes de usar.

### Contraste (WCAG, validado em `scripts/colors.py` e no teste `TestColorContrastAA`)

| Texto / elemento | Fundo | Claro | Escuro | AA |
|---|---|---|---|---|
| `--text` | `--bg` | 15,99 | 16,92 | ok |
| `--text` | `--surface` | 17,57 | 15,90 | ok |
| `--muted` | `--surface` | 7,19 | 6,78 | ok |
| `--muted` | `--code-bg` | 5,94 | 6,11 | ok |
| `--primary` | `--surface` | 5,08 | 5,77 | ok |
| `--primary-text` | `--primary` | 5,13 | 5,92 | ok |
| `--ok` | `--surface` | 5,98 | 8,02 | ok |
| `--warn` | `--surface` | 5,88 | 9,11 | ok |
| `--danger` | `--surface` | 6,48 | 6,54 | ok |
| `--log-text` | `--log-bg` | 14,20 | 15,20 | ok |
| `--border-strong` | `--surface` | 3,98 | 3,94 | ok (UI, 3:1) |

Limite conhecido: `--primary` claro sobre `--code-bg` claro dá 4,2:1 e falha o AA de texto normal. Não coloque link azul sobre o fundo de código ou de badge no tema claro.

## 3. Tipografia

| Função | Família | Licença | Uso |
|---|---|---|---|
| Títulos (`--font-head`) | **Space Grotesk** | SIL OFL | `h1`–`h3`, nome do app, números grandes |
| Texto e UI (`--font-body`) | **IBM Plex Sans** | SIL OFL | Corpo, botões, campos, menus |
| Mono (`--font-mono`) | **IBM Plex Mono** | SIL OFL | Logs, commits, comandos, rótulos técnicos |

Cada pilha cai para fontes do sistema. **Pendente:** os arquivos `.woff2` (latim, variáveis ou só os pesos 400/500/600) ainda não foram embutidos em `web/static/fonts/` nem há `@font-face` no CSS; hoje o painel renderiza com a fonte do sistema. Ao embutir, use `font-display: swap` e subconjunto latim para manter o peso baixo no celular.

| Nível | Tamanho | Peso | Altura de linha | Notas |
|---|---|---|---|---|
| Corpo | 15 px (14 px em desktop com mouse) | 400 | 1,5 | Entrada de formulário fica a 1 rem (evita zoom no iOS) |
| `h1` | 1,5 rem | 600 | herdada | Space Grotesk, tracking −0,01 em |
| `h2` | 1,1 rem | 600 | herdada | idem |
| `h3` | 1 rem | 600 | herdada | idem |
| Rótulo técnico (`dt`, `th`) | 0,72 rem | 500 | 1,4 | IBM Plex Mono, caixa-alta, tracking 0,06 em |
| Pequeno (`.small`) | 0,85 rem | 400 | herdada | |

Regras: no máximo 72 rem de largura de conteúdo; rótulos técnicos só em chaves de lista e cabeçalhos de tabela, nunca em frases; não use itálico nem sublinhado fora de links.

## 4. Espaço e grid

Unidade base de 4 px, escrita em `rem` no CSS (espaços recorrentes: .25, .5, .75, 1, 1.5, 2 rem). Conteúdo até 72 rem, centralizado no celular e alinhado à sidebar no desktop. Cards em grade `auto-fill` com mínimo de 18 rem.

### Responsividade (mobile-first)

| Faixa | Navegação | Tabelas | Controles |
|---|---|---|---|
| < 640 px (celular) | Barra superior + menu hambúrguer que abre a lista | Viram cartões com rótulo por célula (`data-label`) | 44 px, texto de entrada a 1 rem |
| 640–899 px (tablet) | Igual ao celular (proposto: não há layout próprio) | Tabela normal | 44 px |
| ≥ 900 px (desktop) | Sidebar fixa de 220 px, sem barra superior | Tabela normal | 36 px **só** com `pointer: fine`; toque mantém 44 px |

Alvos de toque mínimos de 44 px; tablets com toque em tela grande ficam no modo confortável. TV e ultrawide: fora de escopo.

## 5. Forma e elevação

| Token | Valor | Onde |
|---|---|---|
| `--radius-sm` | 2 px | Badges, código inline |
| `--radius-md` | 6 px | Botões, campos, itens de menu, alertas, caixas de comando |
| `--radius-lg` | 10 px | Cards, painéis, tabelas, caixa de login |

Bordas de 1 px. **Sem sombra** (elevação 0 em tudo; modal e menu usam fundo `--surface` e borda). Foco: anel de 3 px em `--focus`, deslocamento de 2 px.

## 6. Ícones

Hoje o painel usa poucos glifos de texto (☰) e a logo em SVG. Para novos ícones: **Lucide** (ISC) ou Tabler (MIT), contorno de traço 1,75 px, grid de 24 px, cor `currentColor`, tamanho 16 px em linha e 20 px em botão de ícone. Não use preenchido, e não use logos de terceiros (Redis, Postgres etc.) coloridos. Inclua o SVG no HTML ou em `static/`: nada vem de CDN. Biblioteca e traço são proposta; ainda não há ícones aplicados.

## 7. Imagens e ilustração

Sem fotografia nem ilustração. A marca é a logo (robô em domo sobre rack, olhos em `>_`), que **não muda** e segue a política de [`TRADEMARKS.md`](../TRADEMARKS.md). Estados vazios usam texto, sem arte. Mascotes e imagens das referências não entram.

## 8. Componentes

Todos existem em `app.css`; as classes abaixo são as reais.

| Componente | Classes | Notas e estados |
|---|---|---|
| Botão | `.btn`, `.primary`, `.danger`, `.ghost`, `.small` | Borda `--border-strong`; hover escurece a borda; `:disabled` a 50 %; foco com anel. Primário é azul cheio (no original era branco invertido) |
| Campo | `input`, `select`, `textarea` | Borda `--border-strong`, rótulo acima (`label`), `readonly` com fundo `--code-bg` |
| Checkbox | `label.check` | 1,3 rem, `accent-color: --primary`, linha de 44 px |
| Card | `.card`, `.cards` | **Régua de status à esquerda** (3 px) por `:has(.st-running / .st-crashed / .st-starting)`: verde, vermelho, âmbar |
| Painel | `.panel` (`.danger`, `.secret`) | Contorno colorido para zona perigosa ou segredo |
| Navegação | `.sidebar`, `.topbar`, `.tabs` | Item ativo: fundo `--code-bg` e **régua de 3 px em `--primary`** à esquerda; abas com sublinhado de 3 px |
| Tabela / lista | `.table`, `.plain`, `.kv` | Cabeçalho e chave em mono caixa-alta; no celular vira cartão |
| Badge | `.badge` + `.st-*`, `.ds-*`, `.lv-*` | Contorno e texto na cor semântica; nunca só cor: sempre com texto ("no ar", "falhou") |
| Alerta | `.flash` (`.ok`, `.err`, `.warn`) | Contorno semântico; `.err` também colore o texto |
| Console de logs | `pre.log` | Fundo `--log-bg`, mono 0,85 rem, quebra de linha, altura máx. 60 vh |
| Estado vazio | `.empty` | Texto centralizado com a próxima ação |

Fora do painel hoje (nos prints, mas sem equivalente no droidpaas): seletor de organização, breadcrumb, tiles de métrica, modal de log. Se forem criados, sigam as regras acima (borda, sem sombra, raios `md`/`lg`).

## 9. Movimento

Sem animação decorativa. Transições, se houver, de 120 ms (cor e borda), curva `ease-out`. `prefers-reduced-motion: reduce` desliga animação, transição e rolagem suave (já está no CSS).

## 10. Tom de voz

Português direto, verbo no infinitivo ou imperativo, sem exclamação e sem jargão de marketing. Estados em palavras curtas: **no ar**, **iniciando**, **falhou**, **em crash loop**. Erro diz o que houve e o que fazer: "Falha no build: faltou `package.json`. Faça push de novo depois de adicioná-lo." Textos vêm do catálogo i18n do painel, não do HTML.

## 11. Origem → Decisão

| Eixo | Extraído da referência (confiança) | Decisão final | Por quê |
|---|---|---|---|
| Fundo escuro | Neutro frio quase preto `#0a0a0a`, cards `#0a0a0a`, elevado `#171717` (alta) | Neutro **quente** `#0e0d0c` / `#161513` / `#211f1c` | Identidade própria; ainda combina com a logo azul |
| Texto | Branco e cinza frio (média) | `#f2efe9` / `#a39d93` quentes | Acompanha os neutros |
| Ação primária | Botão claro invertido, sem cor de marca (alta) | **Azul da marca** `#5b8cff`/`#0b5fff` cheio | Restrição de marca; mais legível ao sol; mudança visual clara |
| Semânticas | Verde `#00c950`, vermelho `#fa2b38`, azul `#234a8e` de badge (alta) | `#4cc26a` / `#ff6b61` / `#e0b04a` (escuro) e `#17722f` / `#b3261e` / `#8a5a00` (claro); sem azul de informação | Mantidos do painel atual, já validados em AA; azul reservado para ação |
| Logs | Linhas inteiras tingidas por nível (alta) | **Não adotado ainda**; mantido o console atual | Vale reavaliar quando houver filtro por nível |
| Tipografia | Sans neo-grotesca tipo Inter/Geist (média, palpite) | Space Grotesk + IBM Plex Sans + IBM Plex Mono | Outra família, licença aberta; mono por consistência |
| Raios | Cerca de 8 / 12 / 16–20 px (média) | 2 / 6 / 10 px | Mais anguloso e compacto |
| Elevação | Só borda, sem sombra (alta) | Mantido (sem sombra), borda de campos mais forte | Mesma lógica, acessibilidade (3:1) nos campos |
| Ícones | Outline fino 24 px, provável Lucide (média) | Lucide/Tabler, traço 1,75 px, **proposta** | Biblioteca aberta; ícones de serviço coloridos fora |
| Densidade | Compacta, texto de 13–14 px (média) | 14 px com mouse ≥ 900 px; 15 px e 44 px no toque | Celular é o uso principal |
| Estado nos cards | Ponto verde no canto (alta) | **Régua de 3 px** na borda esquerda | Assinatura nº 1 |
| Item ativo da nav | Fundo cinza (alta) | Fundo + **régua azul** de 3 px | Assinatura nº 2 |
| Rótulos | Caixa-alta sans com tracking (média) | Caixa-alta **mono** 0,72 rem | Reforça o ar de terminal |
| Layout | Sidebar com grupos, seletor de organização, breadcrumb (alta) | Sidebar simples sem grupos | Produto de um único usuário e dispositivo |
| Mobile e tablet | Não existe nas referências | Layout atual do painel (proposto) | Derivado, não extraído |
| Logo, nome, textos | Marca de terceiro | **Não usados** | Logo própria; textos próprios |

Assinaturas: (1) régua de status à esquerda do card; (2) régua azul no item ativo e rótulos técnicos em mono caixa-alta.

## 12. Acessibilidade e internacionalização

WCAG 2.2 AA: texto ≥ 4,5:1, componentes de UI ≥ 3:1 (validado por `TestColorContrastAA`, que cobre os dois temas). Foco sempre visível; link "pular para o conteúdo"; estado nunca só por cor; texto ampliável a 200 % (tudo em `rem`, menu sem altura fixa). Idioma: português (pt-BR) hoje, via catálogo i18n; textos longos quebram com `overflow-wrap`. Sem RTL.

## 13. Do / Don't

| Faça | Não faça |
|---|---|
| Azul só em ação, link e foco | Usar azul para "informação" ou decoração |
| Borda de 1 px para separar | Adicionar sombra ou blur |
| Texto no badge ("no ar") além da cor | Indicar estado só por ponto ou régua colorida |
| Mono só em chave de lista, cabeçalho, código e log | Usar mono em frase ou título |
| Comprimir controles só com `pointer: fine` ≥ 900 px | Reduzir alvos de toque no celular |
| Declarar nova cor como token claro **e** escuro, e testar o par | Colocar hex solto em uma regra |
