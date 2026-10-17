#!/usr/bin/env bash
# Roda todas as suítes e2e no aparelho em sequência e resume. Uso (no Termux, com o código em $SRC): bash test/e2e/run-all.sh
cd "$(dirname "$0")" || exit 1
fail=0
for t in 2d-proxy 2e-deploy 2f-git 2g-reconcile 2h-cli 2i-delivery 3-panel; do
  bash "./$t.sh" > "$HOME/e2e-$t.log" 2>&1
  r=$(grep -a "resultado:" "$HOME/e2e-$t.log" | tail -1)
  printf '%-14s %s\n' "$t" "${r:-sem resultado (veja ~/e2e-$t.log)}"
  case "$r" in *" 0 falhas"*) ;; *) fail=1; grep -a FAIL "$HOME/e2e-$t.log" | head -5;; esac
done
exit $fail
