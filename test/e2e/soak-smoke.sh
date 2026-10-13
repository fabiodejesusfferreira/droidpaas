#!/usr/bin/env bash
# Valida o script do soak em escala reduzida (~4 min): apps de exemplo, deploy periódico, restart, kill -9 e o resumo.
. "$(dirname "$0")/lib.sh"
e2e_build
e2e_setup
export DROID_CONFIG=$E2E_HOME/droid.toml
"$E2E_HOME/bin/droid" login --url "http://$PANEL" --token "$TOKEN" >/dev/null
export PATH="$E2E_HOME/bin:$PATH"
export SOAK_OUT=$E2E_HOME/soak DP_HOME=$E2E_HOME/home SOAK_PROXY=$PROXY SOAK_SV=$E2E_HOME/sv SOAK_PREFIX=dpt- SOAK_DROIDD_PID=$(cat "$E2E_HOME/droidd.pid")
export SOAK_HOURS=0.05 SOAK_INTERVAL=5 SOAK_DEPLOY_EVERY=60 SOAK_RESTART_EVERY=45 SOAK_KILL_EVERY=70 SOAK_FIXTURES=$SRC/test/fixtures
rm -rf "$SOAK_OUT"
say "soak em escala reduzida"
bash "$SRC/scripts/soak/soak.sh"
sleep 20   # deploys em segundo plano
EV=$(cat "$SOAK_OUT/events.log")
contains "apps iniciais publicados" "deploy inicial de soaknode ok" "$EV"
contains "go e static também" "deploy inicial de soakgo ok" "$EV"
contains "deploy periódico concluído" "deploy periódico de" "$EV"
contains "restart registrado" "restart de" "$EV"
contains "kill -9 recuperado pelo runit" "kill -9 em" "$EV"
case "$EV" in *"MORTE NÃO INTENCIONAL"*) bad "morte não intencional falsa-positiva:\n$EV";; *) ok "nenhuma morte não intencional";; esac
case "$EV" in *FALHOU*) bad "evento FALHOU:\n$EV";; *) ok "sem falhas";; esac
echo "$EV" | grep "kill -9" | sed 's/^/    /'
OUT=$(python3 "$SRC/scripts/soak/summary.py" "$SOAK_OUT"); contains "resumo gerado" "Mortes não intencionais" "$OUT"; echo "$OUT" | sed 's/^/    /'
check "latências medidas" yes "$(awk -F, 'NR>1 && $5>0 {f=1} END{print f?"yes":"no"}' "$SOAK_OUT/apps.csv")"
e2e_teardown
