#!/data/data/com.termux/files/usr/bin/sh
# Requer: pkg install jq termux-api (e o app Termux:API instalado)
OUT="${1:-$HOME/poc-metrics.csv}"
SVC="${2:-poc-app}"          # nome do serviço runit a observar
INTERVAL="${3:-60}"

[ -f "$OUT" ] || echo "ts,batt_pct,batt_temp_c,plugged,mem_avail_kb,app_rss_kb" > "$OUT"

while :; do
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  b=$(timeout 5 termux-battery-status 2>/dev/null)
  pct=$(printf '%s' "$b"  | jq -r '.percentage // ""')
  tmp=$(printf '%s' "$b"  | jq -r '.temperature // ""')
  plg=$(printf '%s' "$b"  | jq -r '.plugged // ""')
  mem=$(awk '/MemAvailable/ {print $2}' /proc/meminfo)
  pid=$(cat "$PREFIX/var/service/$SVC/supervise/pid" 2>/dev/null)
  rss=""
  [ -n "$pid" ] && rss=$(awk '/VmRSS/ {print $2}' "/proc/$pid/status" 2>/dev/null)
  echo "$ts,$pct,$tmp,$plg,$mem,$rss" >> "$OUT"
  sleep "$INTERVAL"
done
