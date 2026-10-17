/* DroidPaaS — JS mínimo (CSP: sem scripts inline). Tema, confirmações, remoção por digitação e visualizador de logs SSE. */
(function () {
  "use strict";
  var root = document.documentElement;
  try { var th = localStorage.getItem("dp-theme"); if (th) root.dataset.theme = th; } catch (e) {}

  document.addEventListener("click", function (ev) {
    var b = ev.target.closest("[data-theme-toggle]");
    if (b) {
      var dark = getComputedStyle(root).colorScheme.indexOf("dark") === 0;
      var next = dark ? "light" : "dark";
      root.dataset.theme = next;
      try { localStorage.setItem("dp-theme", next); } catch (e) {}
    }
    var c = ev.target.closest("[data-confirm-btn]");
    if (c && !window.confirm(c.getAttribute("data-confirm-btn"))) ev.preventDefault();
  });
  document.addEventListener("submit", function (ev) {
    var msg = ev.target.getAttribute && ev.target.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) ev.preventDefault();
  });

  // exclusão: o botão só habilita quando o nome digitado confere
  document.querySelectorAll("[data-confirm-name]").forEach(function (inp) {
    var btn = inp.form.querySelector("[data-needs-name]");
    inp.addEventListener("input", function () { btn.disabled = inp.value !== inp.getAttribute("data-confirm-name"); });
  });

  // logs ao vivo por SSE. Tudo entra por textContent (nunca innerHTML); ANSI/controles são removidos de novo aqui.
  var ansi = /\u001b\[[0-9;?]*[ -\/]*[@-~]|\u001b\][^\u0007\u001b]*(\u0007|\u001b\\)|[\u0000-\u0008\u000b-\u001f\u007f]/g;
  var MAX = 5000;
  document.querySelectorAll("[data-log]").forEach(function (box) {
    var out = box.querySelector("[data-out]"), follow = box.querySelector("[data-follow]"), filter = box.querySelector("[data-filter]"), status = box.querySelector("[data-status]");
    var lines = [], reload = box.getAttribute("data-reload-on-end") === "1";
    function setStatus(s) { if (status) status.textContent = s; }
    function render() {
      var q = filter && filter.value ? filter.value.toLowerCase() : "";
      out.textContent = lines.filter(function (l) { return !q || l.toLowerCase().indexOf(q) >= 0; }).join("\n");
      if (follow && follow.checked) out.scrollTop = out.scrollHeight;
    }
    if (filter) filter.addEventListener("input", render);
    // rolar para cima pausa o "seguir"
    out.addEventListener("scroll", function () { if (follow && out.scrollTop + out.clientHeight < out.scrollHeight - 40) follow.checked = false; });
    setStatus("conectando…");
    var es = new EventSource(box.getAttribute("data-src"));
    es.onopen = function () { setStatus("ao vivo"); };
    es.addEventListener("line", function (e) {
      var d = JSON.parse(e.data), t = String(d.text).replace(ansi, "");
      lines.push(d.ts ? d.ts + " " + t : t);
      if (lines.length > MAX) lines.splice(0, lines.length - MAX);
      render();
    });
    es.addEventListener("end", function () { es.close(); setStatus("encerrado"); if (reload) setTimeout(function () { location.reload(); }, 600); });
    es.onerror = function () { setStatus("reconectando…"); }; // o EventSource reconecta sozinho com Last-Event-ID
  });
})();
