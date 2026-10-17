# Segurança

O DroidPaaS roda no seu aparelho, executa o código que você publica e pode ficar exposto à internet. Levamos falhas a sério.

## Como reportar uma vulnerabilidade

**Não abra uma issue pública.** Use o botão **Report a vulnerability** na aba *Security* do repositório (aviso privado do GitHub). Se ele não estiver disponível, abra uma issue pedindo um canal de contato, **sem descrever a falha**.

Inclua, se possível: a versão (`droid version`), o que você fez, o que esperava e o que aconteceu, e como reproduzir. Respondemos em até 7 dias e avisamos quando houver correção.

## O que está no escopo

- Painel web (login, sessão, CSRF, XSS, escape de logs), API e tokens.
- Git por SSH/HTTP (`git-shell`, hooks, chaves).
- Armazenamento de segredos (variáveis cifradas, `master.key`, token do túnel).
- Geração da configuração do Caddy e do `cloudflared`.

## O que **não** é uma falha (limites conhecidos)

- **Não há isolamento entre os apps:** todos rodam com o mesmo usuário do Termux. Um app malicioso que você mesmo publicou pode ler os dados dos outros. Hospede só código em que você confia.
- Quem tem acesso ao Termux do aparelho tem acesso a tudo (inclusive à `master.key`).

## Recomendações para quem opera

- Proteja o painel com **Cloudflare Access** (ou Tailscale) ao expô-lo; nunca exponha a API sem autenticação extra e não exponha o `sshd` à internet.
- Guarde o backup (`droid backup --with-key`) como uma senha.
- Mantenha o Termux e os pacotes atualizados (`pkg upgrade`).
