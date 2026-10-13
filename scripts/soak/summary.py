#!/usr/bin/env python3
"""Resume o soak: python3 scripts/soak/summary.py [diretório-do-soak]  (saída em Markdown para colar no relatório)."""
import csv, os, sys, statistics as st
d = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/soak")
m = list(csv.DictReader(open(f"{d}/metrics.csv")))
a = list(csv.DictReader(open(f"{d}/apps.csv")))
ev = open(f"{d}/events.log").read().splitlines() if os.path.exists(f"{d}/events.log") else []
if not m:
    sys.exit("metrics.csv sem amostras")
f = lambda rows, k: [float(r[k]) for r in rows if r.get(k) not in (None, "")]
print(f"Janela: {m[0]['ts']} → {m[-1]['ts']} ({len(m)} amostras)\n")
t, mem, rss, disk = f(m, "batt_temp_c"), f(m, "mem_avail_kb"), f(m, "droidd_rss_kb"), f(m, "disk_kb")
print("| Métrica | Valor |\n|---|---|")
if t: print(f"| Temperatura da bateria | mín {min(t):.1f} · média {st.mean(t):.1f} · **máx {max(t):.1f} °C** |")
if mem: print(f"| RAM disponível | **mín {min(mem)/1024:.0f} MB** · média {st.mean(mem)/1024:.0f} MB |")
if rss:
    q = len(rss) // 4 or 1
    print(f"| RSS do droidd | primeiro quarto {st.mean(rss[:q])/1024:.1f} MB → último quarto {st.mean(rss[-q:])/1024:.1f} MB · **máx {max(rss)/1024:.1f} MB** (RNF-01 ≤ 40 MB) |")
if disk: print(f"| Disco do $DP_HOME | {disk[0]/1024:.0f} MB → {disk[-1]/1024:.0f} MB (+{(disk[-1]-disk[0])/1024:.0f} MB) |")
un = sum(int(r["unintended_restart"] or 0) for r in a)
bad = [r for r in a if r["http_code"] not in ("200", "304")]
lat = sorted(float(r["ms"]) for r in a if r["ms"])
print(f"| **Mortes não intencionais** | **{un}** (critério: 0) |")
print(f"| Sondas HTTP | {len(a)} · fora de 2xx: {len(bad)} ({100*len(bad)/max(len(a),1):.2f} %) |")
if lat: print(f"| Latência (via Caddy) | p50 {lat[len(lat)//2]:.0f} ms · p95 {lat[int(len(lat)*.95)]:.0f} ms · máx {lat[-1]:.0f} ms |")
print("\nEventos relevantes:\n")
for l in ev:
    if any(k in l for k in ("MORTE", "FALHOU", "kill -9", "concluído", "iniciado")): print("-", l)
print(f"\nDeploys disparados: {sum('deploy periódico' in l for l in ev)} · restarts: {sum(l.split(' ',1)[1].startswith('restart de') for l in ev)} · kill -9: {sum('kill -9' in l for l in ev)}")
