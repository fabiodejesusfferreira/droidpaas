# Fase 1 — Prova de Conceito (no Galaxy M22)

**Objetivo:** validar as hipóteses V-01…V-15, medir o aparelho real e decidir **go/no-go** antes de escrever o `droidd`. Roteiro: TSD §16.1 e Apêndice A.9.

## Checklist

- ☑ 1A Ambiente (V-01 e V-14 confirmados; V-08 só o veredito de 24 h no 1G): Termux + API + Boot, pacotes, adb sem fio, phantom killer, isenções de bateria (V-08, V-14) — roteiro: [1A-ambiente.md](1A-ambiente.md)
- ☑ 1B (V-04 confirmada; V-02 e V-09 parciais; `chpst -n` refutada; V-10 parcial) App Node sob `termux-services` + Caddy via Admin API (V-02, V-04, V-09, V-10) — roteiro: [1B-supervisao-proxy.md](1B-supervisao-proxy.md)
- ◐ 1C (Parte A concluída: quick tunnel público OK; Parte B token/wildcard/SSE pendente por falta de domínio) `cloudflared` modo token + wildcard; entrega do token fora do `ps` (V-05, Q-03) — roteiro: [1C-rede-publica.md](1C-rede-publica.md). **Sem domínio na Cloudflare:** Parte A (quick tunnel) agora; Parte B (token + wildcard) pendente
- ☑ 1D (V-06, V-07, V-12 confirmadas) `authorized_keys` com `command=`, `git http-backend`, socket Unix (V-06, V-07, V-12) — roteiro: [1D-git-ssh.md](1D-git-ssh.md)
- ☑ 1E (V-03, V-13, V-14 confirmadas) Go no Termux: versão, SQLite, DNS (V-03, V-13, V-14)
- ☑ 1F (V-11 confirmada para apps puros; nativos sem wheel inviáveis) Builds reais Node e Python (V-11)
- ☑ 1G `scripts/poc/measure.sh` por 24 h + reboot (V-01, V-15) — **67,8 h, 0 reinícios, 0 falhas, temp. máx. 41,7 °C** (ver [poc-report.md](poc-report.md))
- ☑ 1H [poc-report.md](poc-report.md) preenchido e TSD → v0.2 ([../tsd.md](../tsd.md), §2.5)

## Gate go/no-go

Processos vivos após 24 h sem `signal 9` · temperatura abaixo do limite (sugestão 42 °C) · sem OOM em builds · serviços voltam após reboot · acesso público via túnel.

## Resultado

**GO condicional** (ver parecer em [poc-report.md](poc-report.md)): falta o teste de token/wildcard/SSE da Cloudflare, que exige um domínio e só bloqueia a sub-fase 2I.
