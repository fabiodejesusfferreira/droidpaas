package web

import "fmt"

// Catálogo pt-BR: todo texto visível do painel passa por aqui (TSD §12.5 — "textos centralizados para futura tradução").
// Uma chave ausente aparece como a própria chave (visível nos testes, que também conferem os templates contra este mapa).
var ptBR = map[string]string{
	// navegação e layout
	"a11y.skip": "Pular para o conteúdo", "nav.menu": "Menu", "nav.collapse": "Recolher menu", "nav.label": "Navegação principal", "nav.apps": "Apps",
	"nav.new_app": "Novo app", "nav.events": "Eventos", "nav.settings": "Configurações", "nav.logout": "Sair", "theme.toggle": "Tema claro/escuro",
	"tabs.label": "Seções do app", "tabs.overview": "Visão geral", "tabs.deployments": "Deploys", "tabs.environment": "Ambiente",
	"tabs.logs": "Logs", "tabs.domains": "Domínios", "tabs.settings": "Configurações",
	"pager.older": "Mais antigos",

	// login
	"login.title": "Entrar", "login.user": "Usuário", "login.password": "Senha", "login.submit": "Entrar",
	"login.invalid": "Usuário ou senha inválidos.", "login.toomany": "Muitas tentativas. Aguarde alguns minutos e tente de novo.",

	// erros e mensagens gerais
	"err.forbidden.title": "Acesso negado", "err.notfound.title": "Não encontrado", "err.generic.title": "Erro",
	"err.csrf":     "Requisição recusada (token de segurança ausente ou inválido). Recarregue a página e tente de novo.",
	"err.notfound": "A página não existe.", "err.app_not_found": "Esse app não existe.", "err.generic": "Algo deu errado. Tente de novo.",
	"err.back": "Voltar ao início", "err.validation": "Valor inválido em %s: %s", "err.conflict": "Já existe um item com esse nome.",
	"err.no_release": "O app ainda não tem uma versão publicada; faça um deploy primeiro.", "err.not_found": "Item não encontrado.",

	// estados do app
	"state.running": "no ar", "state.starting": "iniciando", "state.stopped": "parado", "state.crashed": "em crash loop",
	"state.deploying": "em deploy", "state.error": "erro", "state.unknown": "desconhecido",
	// status de deploy
	"dstatus.queued": "na fila", "dstatus.running": "em andamento", "dstatus.succeeded": "concluído", "dstatus.failed": "falhou",
	"dstatus.rolled_back": "revertido", "dstatus.cancelled": "cancelado", "dstatus.interrupted": "interrompido",
	"trigger.git_push": "git push", "trigger.manual": "manual", "trigger.rollback": "rollback", "trigger.rebuild": "rebuild",

	// dashboard e dispositivo
	"dash.empty":   "Nenhum app ainda. Crie o primeiro e publique com git push.",
	"device.title": "Dispositivo", "device.loading": "Coletando dados do aparelho…", "device.battery": "Bateria", "device.charging": "carregando",
	"device.discharging": "na bateria", "device.hot": "quente", "device.battery.unavailable": "indisponível (Termux:API ausente ou sem resposta)",
	"device.ram": "RAM", "device.free": "livre", "device.disk": "Disco",
	"card.runtime": "Runtime", "card.port": "porta", "card.release": "Release", "card.norelease": "sem release",
	"stats.usage": "Uso", "stats.rss": "Memória (RSS)", "stats.cpu": "CPU", "stats.procs": "Processos", "stats.none": "Sem dados de uso (o app não está em execução).",

	// assistente
	"wizard.title": "Novo app", "wizard.progress": "Progresso", "wizard.step1": "Nome e branch", "wizard.step2": "Runtime e comandos", "wizard.step3": "Revisar",
	"wizard.name": "Nome", "wizard.name.help": "Letras minúsculas, números e hífen; começa com letra (2 a 31 caracteres).",
	"wizard.branch": "Branch de deploy", "wizard.branch.help": "Só um push neste branch dispara deploy.",
	"wizard.runtime": "Runtime", "wizard.runtime.help": "Automático detecta pelo conteúdo do repositório.",
	"wizard.build": "Comando de build", "wizard.start": "Comando de start", "wizard.health": "Caminho do health check", "wizard.optional": "opcional",
	"wizard.next": "Continuar", "wizard.back": "Voltar", "wizard.create": "Criar app",
	"wizard.nokeys": "Nenhuma chave SSH cadastrada: você não conseguirá dar git push por SSH.", "wizard.nokeys.link": "Cadastrar chave",
	"wizard.err.name":    "Nome inválido: use letras minúsculas, números e hífen (2 a 31 caracteres, começando por letra).",
	"wizard.err.runtime": "Runtime inválido.", "wizard.created": "App %q criado.",
	"runtime.auto": "Automático", "runtime.node": "Node.js", "runtime.python": "Python", "runtime.go": "Go", "runtime.static": "Site estático", "runtime.custom": "Personalizado (Procfile)",
	"publish.intro": "App criado. Falta enviar o código. Siga os passos abaixo:", "publish.ssh": "Por SSH", "publish.http": "Por HTTP",
	"publish.http.help": "(use um token com escopo git como senha)", "publish.watch": "O build e o deploy aparecem no próprio terminal do git push.",
	"publish.open": "Abrir o app",
	"publish.s1":   "Cadastre sua chave SSH (uma vez só)", "publish.s1.body": "Ela autoriza o seu computador a enviar código para este aparelho. Primeiro, no terminal do seu computador, mostre a chave pública (o texto começa com ssh-ed25519; se o comando disser que o arquivo não existe, crie a chave com ssh-keygen -t ed25519 e rode de novo) e copie tudo:",
	"publish.s1.link": "Abrir a página Chaves SSH", "publish.s1.where": "No menu do painel (no celular, o botão ☰ no topo), toque em Configurações e, nas abas de cima, em Chaves SSH. Cole o texto no campo Chave pública, dê um nome (ex.: meu-laptop) e toque em Adicionar chave. O botão abaixo leva direto para essa página.", "publish.s2": "Envie o código com git push", "publish.s2.body": "No terminal do seu computador, dentro da pasta do projeto (que precisa ser um repositório git com pelo menos um commit):",
	"publish.ip": "Troque IP-DO-CELULAR pelo endereço do aparelho na rede (Wi-Fi → detalhes da rede, ou ifconfig no Termux). Você abriu o painel por localhost, por isso o endereço real não aparece aqui.",
	"publish.s3": "Acompanhe o deploy", "publish.s3.body": "O build roda no aparelho e o log aparece no terminal do git push. Também dá para ver em Deploys, pelo painel. Se o build passar e o app responder, ele entra no ar; se falhar, a versão anterior continua no ar.",
	"publish.s4": "O que o seu app precisa ter", "publish.s4.body": "Node: um package.json com o script start. Python: requirements.txt e um Procfile com web:. Go: go.mod. Site estático: index.html. O servidor deve escutar em HOST e PORT (variáveis de ambiente) e, de preferência, responder 200 em /health.",
	"publish.s5": "Abrir o app", "publish.s5.body": "Depois do deploy, o app responde em:", "publish.s5.nodomain": "Ainda não há domínio. Cadastre um em Domínios, ou defina o domínio base em Configurações.",

	// visão geral e ações
	"overview.release": "Release atual", "overview.commit": "Commit", "overview.when": "Quando", "overview.resources": "Recursos",
	"overview.last": "Últimos deploys", "overview.all": "Ver todos", "overview.howto": "Como publicar",
	"action.deploy": "Deploy", "action.restart": "Reiniciar", "action.start": "Iniciar", "action.stop": "Parar",
	"action.stop.confirm": "Parar este app?", "action.start.ok": "App iniciado.", "action.stop.ok": "App parado.", "action.restart.ok": "App reiniciado.",

	// deploys
	"deploys.new": "Novo deploy", "deploys.ref": "Branch, tag ou commit", "deploys.rebuild": "Reconstruir ignorando caches de dependências",
	"deploys.rollback": "Rollback", "deploys.rollback.to": "Voltar para", "deploys.rollback.none": "Não há release anterior disponível.",
	"deploys.rollback.confirm": "Voltar para a release escolhida?", "deploys.history": "Histórico", "deploys.none": "Nenhum deploy ainda.",
	"deploys.status": "Status", "deploys.trigger": "Origem", "deploys.duration": "Duração", "deploys.current": "atual", "deploys.deploy": "Deploy",
	"deploys.stage": "Etapa", "deploys.error": "Erro", "deploys.cancel": "Cancelar deploy", "deploys.cancel.confirm": "Cancelar este deploy?",
	"deploys.cancel.ok": "Cancelamento solicitado.", "deploys.back": "Voltar ao histórico",
	"deploy.err.bad_ref": "Esse branch/commit não existe no repositório (faça um git push primeiro).", "deploy.err.no_previous": "Não há release anterior para o rollback.",
	"deploy.err.bad_release": "Release inválida para rollback.", "deploy.err.not_active": "O deploy não está em andamento.",
	"deploy.err.too_late": "O deploy já passou do ponto de troca e não pode ser cancelado.",

	// logs
	"logs.label": "Saída de log", "logs.follow": "Seguir", "logs.filter": "Filtrar", "logs.download": "Baixar últimas linhas",
	"logs.connecting": "conectando…", "logs.live": "ao vivo", "logs.ended": "encerrado", "logs.reconnecting": "reconectando…",

	// ambiente
	"env.help": "Variáveis de ambiente do app. Segredos aparecem mascarados. As alterações valem no próximo início do app.",
	"env.key":  "Nome", "env.value": "Valor", "env.secret": "Segredo", "env.remove": "Remover", "env.reveal": "Revelar", "env.new": "Nova variável",
	"env.save": "Salvar", "env.save_restart": "Salvar e reiniciar", "env.saved": "Variáveis salvas. Reinicie o app para valerem.",
	"env.saved_restarted": "Variáveis salvas e app reiniciado.", "env.nochange": "Nada mudou.",

	// domínios
	"domains.auto": "automático", "domains.none": "Nenhum domínio.", "domains.add": "Novo domínio", "domains.add.btn": "Adicionar",
	"domains.added": "Domínio adicionado.", "domains.removed": "Domínio removido.", "domains.remove.confirm": "Remover este domínio?",
	"domains.help.title": "Como fazer o domínio funcionar", "domains.help.tunnel": "No túnel da Cloudflare, crie um hostname público apontando para",
	"domains.help.base": "O domínio automático usa o wildcard", "domains.help.nobase": "Defina o domínio base em", "domains.help.status": "Estado do túnel",

	// configurações do app
	"settings.save": "Salvar", "settings.saved": "Configurações salvas.", "settings.health_timeout": "Tempo limite do health check (s)",
	"settings.static_dir": "Diretório estático", "settings.nice": "Prioridade (nice)", "settings.nice.help": "Sem efeito no Android 13; mantido por compatibilidade.",
	"settings.danger": "Zona de perigo", "settings.danger.help": "Remover apaga o serviço, o repositório, as releases, as variáveis e os logs. Não dá para desfazer.",
	"settings.danger.type": "Para confirmar, digite o nome do app:", "settings.delete": "Remover app", "app.deleted": "App %q removido.",

	// configurações globais
	"settings.tabs": "Seções das configurações", "stab.general": "Geral", "stab.tunnel": "Túnel", "stab.keys": "Chaves SSH", "stab.tokens": "Tokens", "stab.account": "Conta",
	"settings.base_domain": "Domínio base", "settings.base_domain.help": "Cada app ganha <app>.<domínio base>. Deixe vazio para desativar.",
	"settings.panel_hostname": "Hostname do painel", "settings.panel_hostname.help": "Opcional: serve o painel por este hostname (proteja-o com Cloudflare Access).",
	"settings.keep_releases": "Releases mantidas por app", "settings.git_http": "Permitir git push por HTTP",
	"tunnel.state": "Estado do túnel", "tunnel.help": "O DroidPaaS roda o cloudflared em modo token; você cria o túnel no painel da Cloudflare.",
	"tunnel.step1": "Na Cloudflare: Zero Trust → Networks → Tunnels → crie um túnel e copie o token.",
	"tunnel.step2": "Crie um hostname público", "tunnel.step3": "Cole o token abaixo.", "tunnel.token": "Token do túnel", "tunnel.token.set": "token já configurado (digite para trocar)",
	"tunnel.save": "Salvar token", "tunnel.clear": "Remover token", "tunnel.clear.confirm": "Remover o token e desligar o túnel?",
	"tunnel.saved": "Token salvo; o túnel sobe em instantes.", "tunnel.cleared": "Token removido; túnel desligado.", "tunnel.err.empty": "Cole o token do túnel.",
	"tunnel.state.not_configured": "não configurado", "tunnel.state.down": "desligado", "tunnel.state.connecting": "conectando", "tunnel.state.connected": "conectado", "tunnel.state.unknown": "desconhecido",
	"keys.help": "Chaves públicas autorizadas a dar git push por SSH (somente git; não dão acesso a um shell).", "keys.none": "Nenhuma chave cadastrada.",
	"keys.name": "Nome", "keys.public": "Chave pública", "keys.add": "Adicionar chave", "keys.added": "Chave adicionada (%s).", "keys.removed": "Chave removida.",
	"keys.remove.confirm": "Remover esta chave?", "keys.exists": "Essa chave já está cadastrada.",
	"tokens.once": "Copie o token agora: ele não será mostrado de novo.", "tokens.prefix": "Prefixo", "tokens.scope": "Escopo",
	"tokens.scope.full": "full (API completa)", "tokens.scope.git": "git (só git push/clone por HTTP)", "tokens.revoked": "revogado", "tokens.active": "ativo",
	"tokens.revoke": "Revogar", "tokens.revoke.confirm": "Revogar este token?", "tokens.create": "Criar token", "tokens.created": "Token criado.", "tokens.revoked.ok": "Token revogado.",
	"account.sessions": "Sessões ativas", "account.current": "Senha atual", "account.new": "Nova senha", "account.new.help": "Mínimo de 12 caracteres.",
	"account.confirm": "Repita a nova senha", "account.change": "Trocar senha", "account.err.current": "A senha atual não confere.",
	"account.err.mismatch": "A confirmação não confere com a nova senha.", "account.err.short": "A nova senha precisa ter ao menos %d caracteres.",
	"account.changed": "Senha alterada; %d outra(s) sessão(ões) encerrada(s).",

	// eventos
	"events.none": "Nenhum evento.", "events.when": "Quando", "events.level": "Nível", "events.kind": "Tipo", "events.message": "Mensagem", "events.details": "Detalhes",
}

// T traduz uma chave; chave ausente devolve a própria chave.
func T(key string) string {
	if s, ok := ptBR[key]; ok {
		return s
	}
	return key
}

// TR traduz quando o texto é uma chave do catálogo e devolve o texto como veio quando não é.
func TR(s string) string {
	if v, ok := ptBR[s]; ok {
		return v
	}
	return s
}

var _ = fmt.Sprintf
