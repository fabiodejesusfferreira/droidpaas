# Relatório da Fase 1 (PoC)

Aparelho: Samsung Galaxy M22 (`SM-M225FV`) · Android 13 (SDK 33) · kernel `4.14.186` aarch64 · RAM total 3,8 GB (`MemAvailable` ≈ 1,5 GB com o sistema em repouso) · Go `1.27.1 android/arm64`. Versão do Termux: _a preencher_.

| ID | Hipótese | Resultado (confirmada/refutada/ajustada) | Evidência (comando/saída) | Decisão |
|---|---|---|---|---|
| V-01 | `termux-battery-status` retorna temperatura e % com a tela apagada | **Confirmada**: JSON com `temperature` (32,8 °C) e `percentage` (63); `current` varia entre chamadas; repetido com a tela apagada (~10 min) sem problema | `termux-battery-status` | Widget do dispositivo (RF-39) mantido |
| V-02 | `termux-services` sobe `runsvdir` no boot e detecta serviços em poucos segundos | Parcial: `runsvdir` roda com `SVDIR=$PREFIX/var/service`. Serviço criado por `mv` de pasta oculta foi detectado em **≤ ~3 s** (`sv status` mostrou o `runsv` com 3 s de vida; o cronômetro do roteiro marcou 0 s porque `start` foi definido tarde demais, corrigido no roteiro). `sv up/stop`, `kill -9` → novo PID em ≤ 1 s. **Falta** o boot (bloco 1G) | `sv status poc-app` → `down: poc-app: 3s; run: log: (pid 26802) 3s`; `kill -9 26814` → `run: poc-app: (pid 26833) 1s` | `termux-services` mantido (ADR-03/Q-02 tende a "manter"; fechar após o boot no 1G) |
| V-03 | `modernc.org/sqlite` compila e roda no Termux | | | |
| V-04 | `caddy` aceita config JSON via Admin API sem HTTPS automático | **Confirmada**: Caddy sobe como serviço runit com `bootstrap.json`; Admin API em `127.0.0.1:2019`; `POST /load` → 200 (config completa); roteamento por `Host` (app → proxy, `blog.local` → `file_server`, `parado.local` → 503, host desconhecido → 404); após `sv restart poc-caddy` a rota `poc.local` some (404) e volta com novo `POST /load` | `/health` → `ok`; `blog.local` → `<h1>estatico</h1>`; `parado.local` → `HTTP/1.1 503`; pós-restart → `404`, depois do reload → `ok`; log: `automatic HTTPS is completely disabled` | ADR-05 (config completa por `POST /load`) mantido; o reconciliador **deve** reaplicar a config quando o `dp-caddy` reinicia (§8.5.3) |
| V-05 | `cloudflared` modo token + hostname wildcard | | | |
| V-06 | `sshd` respeita `command=` forçado; `git-receive-pack` funciona | | | |
| V-07 | `git http-backend` disponível | | | |
| V-08 | Phantom process killer desativável via adb; processos sobrevivem 24 h | Parcial: **desativação confirmada** no Android 13 (`dumpsys` mostra `max_phantom_processes=2147483647`). 41 `sleep` em segundo plano sobreviveram a ~10 min de tela apagada. **Falta** o veredito das 24 h (bloco 1G) | `adb pair localhost:<porta>` → `adb connect localhost:<porta da tela principal>` → `device_config set_sync_disabled_for_tests persistent` → `device_config put activity_manager max_phantom_processes 2147483647` → `settings put global settings_enable_monitor_phantom_procs false` | Pendente (persistência após reboot a conferir) |
| V-09 | `runit` fornece `sv`, `chpst`, `svlogd`; `chpst -n` funciona | Parcial: `chpst -e` **OK** (`FOO=bar` chegou ao app); `svlogd -tt` **OK** (`2026-10-10_01:11:53.26671 listening on 20001`; rotação não testada). **`chpst -n` REFUTADA no Android 13**: nem `chpst -n 10` nem `nice -n 10` alteram o nice (campo 19 de `/proc/<pid>/stat` fica 0; `ps` também mostra `NI 0`), sem mensagem de erro | `chpst -n 10 sh -c 'cut -d" " -f19 /proc/$$/stat'` → `0`; `nice -n 10 sh -c '...'` → `0` | `nice` vira **best-effort/sem efeito** no Termux: manter a coluna `apps.nice` e o `chpst -n` (inofensivo), mas **não** contar com ele; a proteção contra builds pesados é a fila de 1 + limites do runtime (`NODE_OPTIONS=--max-old-space-size`, `GOMAXPROCS`). Atualizar RNF/TSD v0.2 |
| V-10 | `/proc/<pid>/status` e `/proc/stat` legíveis | **Parcialmente refutada**: `/proc/<pid>/status` (VmRSS 34 MB do node) e `/proc/<pid>/stat` (utime/stime) **OK**; `/proc/meminfo` OK; **`/proc/stat` e `/proc/loadavg`: Permission denied** | `grep VmRSS /proc/$PID/status` → `34036 kB`; `cut -d' ' -f14,15 /proc/$PID/stat` → `72 6`; `head -1 /proc/stat` → Permission denied | RF-42 mantido (RSS/CPU por app via `/proc/<pid>`; CPU = delta de utime+stime sobre tempo de relógio). CPU **global** e load average indisponíveis: `measure.sh` não usa mais `loadavg` |
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
- **Leituras de `/proc` (V-10):** `/proc/stat` e `/proc/loadavg` são negados no Android 13. O `DeviceProbe` real (Fase 3) calcula CPU por app só com `/proc/<pid>/stat`; não há CPU global. `scripts/poc/measure.sh` perdeu a coluna `load1`.
- **Caddy no Termux:** o log mostrou `GOMAXPROCS=8`, `GOMEMLIMIT` automático de ~3,5 GB e armazenamento em `~/caddy`; config autosalva em `~/.config/caddy/autosave.json`. Na Fase 2, definir `XDG_*`/`HOME` do `dp-caddy` sob `$DP_HOME`, fixar `GOMEMLIMIT` menor e **não** usar `--resume`. RSS do Caddy ocioso: **35 MB** (VmRSS 35432 kB). Referência: app Node mínimo ≈ 34 MB.
- Rastreio dos desvios dos demais blocos: _a preencher_.
