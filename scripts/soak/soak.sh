#!/usr/bin/env bash
# Soak test do DroidPaaS (RNF-10, TSD §17.5): 3–5 apps de exemplo sob carga leve, um deploy a cada poucas horas,
# reinícios aleatórios e métricas contínuas. Rode numa instalação real, com `droid login` já feito:
#   nohup bash scripts/soak/soak.sh >/dev/null 2>&1 &         # 72 h por padrão
# Variáveis: SOAK_HOURS=72  SOAK_INTERVAL=60  SOAK_DEPLOY_EVERY=14400  SOAK_RESTART_EVERY=10800  SOAK_KILL_EVERY=28800
#            DP_HOME=~/droidpaas  SOAK_PROXY=127.0.0.1:18080  SOAK_SV=$PREFIX/var/service  SOAK_PREFIX=dp-  SOAK_OUT=~/soak
set -u
export PATH=/data/data/com.termux/files/usr/bin:$PATH
HOURS=${SOAK_HOURS:-72}; INTERVAL=${SOAK_INTERVAL:-60}
DEPLOY_EVERY=${SOAK_DEPLOY_EVERY:-14400}; RESTART_EVERY=${SOAK_RESTART_EVERY:-10800}; KILL_EVERY=${SOAK_KILL_EVERY:-28800}
DP_HOME=${DP_HOME:-$HOME/droidpaas}; PROXY=${SOAK_PROXY:-127.0.0.1:18080}
SV=${SOAK_SV:-${PREFIX:-}/var/service}; PFX=${SOAK_PREFIX:-dp-}; OUT=${SOAK_OUT:-$HOME/soak}
FX=${SOAK_FIXTURES:-$(cd "$(dirname "$0")/../.." && pwd)/test/fixtures}
mkdir -p "$OUT"; METRICS=$OUT/metrics.csv; APPS_CSV=$OUT/apps.csv; EVENTS=$OUT/events.log; W=$OUT/work
APPS="soaknode:node-build soakpy:python soakgo:go soaksite:static"

ev() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >> "$EVENTS"; }
svpid() { sv status "$SV/$1" 2>/dev/null | grep -o 'pid [0-9]*' | head -1 | cut -d' ' -f2; }
rss() { [ -n "${1:-}" ] && awk '/VmRSS/{print $2}' "/proc/$1/status" 2>/dev/null; }
now() { date +%s; }

# ---- preparo: cria os apps, publica os fixtures e espera o primeiro deploy ----
setup() {
  mkdir -p "$W"
  for spec in $APPS; do
    app=${spec%%:*}; fx=${spec##*:}
    droid apps create "$app" >/dev/null 2>&1 || true
    droid domains "$app" add "$app.soak.test" >/dev/null 2>&1 || true
    rm -rf "$W/$app"; cp -r "$FX/$fx" "$W/$app"
    git -C "$W/$app" init -q -b main
    git -C "$W/$app" -c user.name=soak -c user.email=soak@soak add -A
    git -C "$W/$app" -c user.name=soak -c user.email=soak@soak commit -qm "soak v0"
    # o push para o repo bare local dispara o post-receive e o deploy (síncrono: só volta quando o deploy termina)
    if git -C "$W/$app" push -q -f "$DP_HOME/apps/$app/repo.git" main >>"$OUT/push-$app.log" 2>&1; then ev "deploy inicial de $app ok"; else ev "deploy inicial de $app FALHOU (veja push-$app.log)"; fi
  done
}

[ -f "$METRICS" ] || echo "ts,batt_pct,batt_temp_c,plugged,mem_avail_kb,droidd_rss_kb,caddy_rss_kb,disk_kb" > "$METRICS"
[ -f "$APPS_CSV" ] || echo "ts,app,pid,http_code,ms,unintended_restart" > "$APPS_CSV"
ev "soak iniciado: ${HOURS} h, intervalo ${INTERVAL} s"
setup

declare -A last_pid expected
start=$(now); end=$(awk -v h="$HOURS" -v s="$start" 'BEGIN{printf "%d", s + h*3600}'); t_deploy=$start; t_restart=$start; t_kill=$start; n=0
while [ "$(now)" -lt "$end" ]; do
  t=$(now); ts=$(date -u +%FT%TZ)
  b=$(timeout 5 termux-battery-status 2>/dev/null)
  pct=$(printf %s "$b" | jq -r '.percentage // ""' 2>/dev/null); tmp=$(printf %s "$b" | jq -r '.temperature // ""' 2>/dev/null); plg=$(printf %s "$b" | jq -r '.plugged // ""' 2>/dev/null)
  echo "$ts,$pct,$tmp,$plg,$(awk '/MemAvailable/{print $2}' /proc/meminfo),$(rss "${SOAK_DROIDD_PID:-$(svpid droidd)}"),$(rss "$(svpid ${PFX}caddy)"),$(du -sk "$DP_HOME" 2>/dev/null | cut -f1)" >> "$METRICS"

  for spec in $APPS; do
    app=${spec%%:*}
    pid=$(svpid "${PFX}app-$app"); unint=0
    if [ -n "${last_pid[$app]:-}" ] && [ "$pid" != "${last_pid[$app]}" ]; then
      # reinício que não foi pedido por este script nos últimos 2 min = morte não intencional
      if [ $(( t - ${expected[$app]:-0} )) -gt 120 ]; then unint=1; ev "MORTE NÃO INTENCIONAL de $app (pid ${last_pid[$app]} → $pid)"; fi
    fi
    last_pid[$app]=$pid
    r=$(curl -s -o /dev/null -m 10 -w '%{http_code} %{time_total}' -H "Host: $app.soak.test" "http://$PROXY/")
    echo "$ts,$app,$pid,${r% *},$(awk -v s="${r#* }" 'BEGIN{printf "%d", s*1000}'),$unint" >> "$APPS_CSV"
  done

  if [ $(( t - t_deploy )) -ge "$DEPLOY_EVERY" ]; then  # novo commit + deploy num app (rodízio)
    t_deploy=$t; n=$((n+1)); app=soaknode; [ $((n%2)) -eq 0 ] && app=soakpy
    if [ $app = soaknode ]; then echo "// soak $n" >> "$W/$app/server.js"; else echo "# soak $n" >> "$W/$app/server.py"; fi
    git -C "$W/$app" -c user.name=soak -c user.email=soak@soak commit -qam "soak $n" 2>/dev/null
    expected[$app]=$t
    ev "deploy periódico #$n de $app disparado"
    ( git -C "$W/$app" push -q -f "$DP_HOME/apps/$app/repo.git" main >>"$OUT/push-$app.log" 2>&1 && ev "deploy periódico de $app concluído" || ev "deploy periódico de $app FALHOU" ) &
  fi
  if [ $(( t - t_restart )) -ge "$RESTART_EVERY" ]; then
    t_restart=$t; app=$(printf '%s\n' soaknode soakpy soakgo | shuf -n1)
    expected[$app]=$t; droid restart "$app" >/dev/null 2>&1 && ev "restart de $app" || ev "restart de $app FALHOU"
  fi
  if [ $(( t - t_kill )) -ge "$KILL_EVERY" ]; then   # kill -9 (RNF-06: o runit precisa reiniciar em ≤ 5 s)
    t_kill=$t; app=$(printf '%s\n' soaknode soakpy soakgo | shuf -n1); p=$(svpid "${PFX}app-$app")
    if [ -n "$p" ]; then
      expected[$app]=$t; kill -9 "$p"; k0=$(date +%s%N)
      for _ in $(seq 1 50); do np=$(svpid "${PFX}app-$app"); [ -n "$np" ] && [ "$np" != "$p" ] && break; sleep 0.2; done
      ev "kill -9 em $app: reiniciou em $(( ($(date +%s%N) - k0) / 1000000 )) ms"
    fi
  fi
  sleep "$INTERVAL"
done
ev "soak concluído"
