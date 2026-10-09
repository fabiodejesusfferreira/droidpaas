// Modos via BROKEN_MODE: crash (sai com erro), noport (vive sem abrir porta), loop (padrão: cai em loop).
const mode = process.env.BROKEN_MODE || "loop";
if (mode === "noport") setInterval(() => {}, 1000);
else {
  console.error("fixture broken: falhando de propósito (" + mode + ")");
  process.exit(1);
}
