# Fase 1 — Prova de Conceito (no Galaxy M22)

**Objetivo:** validar as hipóteses V-01…V-15, medir o aparelho real e decidir **go/no-go** antes de escrever o `droidd`. Roteiro: TSD §16.1 e Apêndice A.9.

## Checklist

- ☐ 1A Ambiente: Termux + API + Boot, pacotes, adb sem fio, phantom killer, isenções de bateria (V-08, V-14)
- ☐ 1B App Node sob `termux-services` + Caddy via Admin API (V-02, V-04, V-09, V-10)
- ☐ 1C `cloudflared` modo token + wildcard; entrega do token fora do `ps` (V-05, Q-03)
- ☐ 1D `authorized_keys` com `command=`, `git http-backend`, socket Unix (V-06, V-07, V-12)
- ☐ 1E Go no Termux: versão, SQLite, DNS (V-03, V-13, V-14)
- ☐ 1F Builds reais Node e Python (V-11)
- ☐ 1G `scripts/poc/measure.sh` por 24 h + reboot (V-01, V-15)
- ☐ 1H [poc-report.md](poc-report.md) preenchido e TSD → v0.2

## Gate go/no-go

Processos vivos após 24 h sem `signal 9` · temperatura abaixo do limite (sugestão 42 °C) · sem OOM em builds · serviços voltam após reboot · acesso público via túnel.

## Resultado

_A preencher._
