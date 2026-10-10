#!/data/data/com.termux/files/usr/bin/sh
# Requer: pkg install jq termux-api curl (e o app Termux:API instalado)
# Uso: measure.sh [csv] [serviço-principal] [intervalo-s]
# Um pid que muda entre linhas = o serviço reiniciou (morte não intencional se ninguém pediu).
OUT="${1:-$HOME/poc-metrics.csv}"
SVC="${2:-poc-app}"          # nome do serviço runit a observar
INTERVAL="${3:-60}"
SVDIR="${SVDIR:-$PREFIX/var/service}"
PROBE_URL="${PROBE_URL:-http://127.0.0.1:8080/health}"  # via proxy
PROBE_HOST="${PROBE_HOST:-poc.local}"
OTHERS="${OTHERS:-poc-caddy poc-cloudflared}"            # pids extras (2 serviços)

[ -f "$OUT" ] || echo "ts,batt_pct,batt_temp_c,plugged,mem_avail_kb,app_pid,svc2_pid,svc3_pid,app_rss_kb,http_code" > "$OUT"

pidof_svc() { cat "$SVDIR/$1/supervise/pid" 2>/dev/null; }

while :; do
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  b=$(timeout 5 termux-battery-status 2>/dev/null)
  pct=$(printf '%s' "$b"  | jq -r '.percentage // ""')
  tmp=$(printf '%s' "$b"  | jq -r '.temperature // ""')
  plg=$(printf '%s' "$b"  | jq -r '.plugged // ""')
  mem=$(awk '/MemAvailable/ {print $2}' /proc/meminfo)
  pid=$(pidof_svc "$SVC")
  set -- $OTHERS
  p2=$(pidof_svc "$1"); p3=$(pidof_svc "$2")
  rss=""
  [ -n "$pid" ] && rss=$(awk '/VmRSS/ {print $2}' "/proc/$pid/status" 2>/dev/null)
  code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' -H "Host: $PROBE_HOST" "$PROBE_URL" 2>/dev/null)
  echo "$ts,$pct,$tmp,$plg,$mem,$pid,$p2,$p3,$rss,$code" >> "$OUT"
  sleep "$INTERVAL"
done
