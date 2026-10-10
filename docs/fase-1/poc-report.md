# Relatório da Fase 1 (PoC)

Aparelho: Samsung Galaxy M22 (`SM-M225FV`) · Android 13 (SDK 33) · kernel `4.14.186` aarch64 · RAM total 3,8 GB (`MemAvailable` ≈ 1,5 GB com o sistema em repouso) · Go `1.27.1 android/arm64`. Versão do Termux: _a preencher_.

| ID | Hipótese | Resultado (confirmada/refutada/ajustada) | Evidência (comando/saída) | Decisão |
|---|---|---|---|---|
| V-01 | `termux-battery-status` retorna temperatura e % com a tela apagada | **Confirmada**: JSON com `temperature` (32,8 °C) e `percentage` (63); `current` varia entre chamadas; repetido com a tela apagada (~10 min) sem problema | `termux-battery-status` | Widget do dispositivo (RF-39) mantido |
| V-02 | `termux-services` sobe `runsvdir` no boot e detecta serviços em poucos segundos | | | |
| V-03 | `modernc.org/sqlite` compila e roda no Termux | | | |
| V-04 | `caddy` aceita config JSON via Admin API sem HTTPS automático | | | |
| V-05 | `cloudflared` modo token + hostname wildcard | | | |
| V-06 | `sshd` respeita `command=` forçado; `git-receive-pack` funciona | | | |
| V-07 | `git http-backend` disponível | | | |
| V-08 | Phantom process killer desativável via adb; processos sobrevivem 24 h | Parcial: **desativação confirmada** no Android 13 (`dumpsys` mostra `max_phantom_processes=2147483647`). 41 `sleep` em segundo plano sobreviveram a ~10 min de tela apagada. **Falta** o veredito das 24 h (bloco 1G) | `adb pair localhost:<porta>` → `adb connect localhost:<porta da tela principal>` → `device_config set_sync_disabled_for_tests persistent` → `device_config put activity_manager max_phantom_processes 2147483647` → `settings put global settings_enable_monitor_phantom_procs false` | Pendente (persistência após reboot a conferir) |
| V-09 | `runit` fornece `sv`, `chpst`, `svlogd`; `chpst -n` funciona | Parcial: `sv`, `chpst`, `svlogd` e `runsvdir` presentes em `$PREFIX/bin`. `chpst -n` testa no 1B | `which sv chpst svlogd runsvdir` (o `which` precisou de `pkg install which`) | Pendente |
| V-10 | `/proc/<pid>/status` e `/proc/stat` legíveis | | | |
| V-11 | Builds Node e Python completam sem OOM | | | |
| V-12 | Socket Unix em `$HOME` funciona | | | |
| V-13 | Binário Go resolve DNS | | | |
| V-14 | Go do Termux ≥ 1.22 | **Confirmada**: Go 1.27.1 (`GOOS=android`, `arm64`) | `go version` | `ServeMux` com padrões disponível; sem roteador de terceiros |
| V-15 | Termux:Boot executa no boot sem bloqueio do One UI | | | |

## Medições (24 h)

_Resumo do CSV de `measure.sh`: temperatura máx., RAM mínima disponível, reinícios._

## Decisões alteradas em relação ao TSD

- **Build do `droidd` (V-13/R-13):** o Go do Termux é `GOOS=android`, não `linux`. O alvo `build-android` do `Makefile` usa `GOOS=linux GOARCH=arm64`, que gera outro tipo de binário e pode ter o problema de DNS (C-07). Decisão provisória: compilar **no aparelho** (premissa P-05) e tratar o `build-android` como não validado até o V-13.
- **Disponibilidade de pacotes (V-04/V-05/V-06):** `caddy`, `cloudflared`, `node`, `python`, `git` e `sshd` instalam sem problema. Isso só prova que existem; o comportamento é testado nos blocos 1B–1D.
- Isenção de bateria aplicada no Termux (1A).
- Rastreio dos desvios dos demais blocos: _a preencher_.
