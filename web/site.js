// Small interactions shared by both pages.
(function () {
  "use strict";

  // Copy buttons.
  document.querySelectorAll("[data-copy]").forEach(function (b) {
    b.addEventListener("click", function () {
      var el = document.getElementById(b.getAttribute("data-copy"));
      var text = el ? el.textContent : "";
      var done = function () { b.textContent = "Copied"; setTimeout(function () { b.textContent = "Copy"; }, 1600); };
      if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, function () {}); else done();
    });
  });

  // Tabs.
  document.querySelectorAll("[role=tablist]").forEach(function (list) {
    var tabs = Array.prototype.slice.call(list.querySelectorAll("[role=tab]"));
    function select(t, focus) {
      tabs.forEach(function (x) {
        var on = x === t;
        x.setAttribute("aria-selected", on); x.tabIndex = on ? 0 : -1;
        document.getElementById(x.getAttribute("aria-controls")).hidden = !on;
      });
      if (focus) t.focus();
    }
    tabs.forEach(function (t, i) {
      t.addEventListener("click", function () { select(t); });
      t.addEventListener("keydown", function (e) {
        var d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
        if (d) { e.preventDefault(); select(tabs[(i + d + tabs.length) % tabs.length], true); }
      });
    });
  });

  // Event-log timeline on the home page.
  var events = document.getElementById("events");
  if (events) {
    var cmd = document.getElementById("tl-cmd"), note = document.getElementById("tl-note");
    var buttons = events.querySelectorAll("button");
    function show(b) {
      buttons.forEach(function (x) { x.setAttribute("aria-pressed", x === b); });
      var seq = b.dataset.seq, node = b.dataset.node;
      switch (b.dataset.kind) {
        case "start":
          cmd.textContent = "stiggy start -ns newsroom article -input '{\"topic\": \"NATS\"}'";
          note.textContent = "Starting prints the execution id. Pass -id to make the start idempotent.";
          break;
        case "node":
          cmd.textContent = "stiggy get <exec> -seq " + seq + "\nstiggy rerun <exec> " + node;
          note.textContent = "The state right after event " + seq + ", or run again from " + node + " after changing its prompt.";
          break;
        case "human":
          cmd.textContent = "stiggy fork <exec> 12\nstiggy resume <fork> review -value '\"keep the intro\"'";
          note.textContent = "Branch from just before the answer and give a different one. The original execution is untouched.";
          break;
        default:
          cmd.textContent = "stiggy history <exec>";
          note.textContent = "The whole event log, as JSON lines: every step, route, answer and token count.";
      }
    }
    buttons.forEach(function (b) { b.addEventListener("click", function () { show(b); }); });
    show(buttons[3]);
  }

  // Guide: section filter and scroll spy.
  var filter = document.getElementById("toc-filter");
  var toc = document.getElementById("toc");
  if (toc) {
    var links = Array.prototype.slice.call(toc.querySelectorAll("a"));
    if (filter) {
      filter.addEventListener("input", function () {
        var q = filter.value.trim().toLowerCase();
        links.forEach(function (a) {
          var sec = document.getElementById(a.hash.slice(1));
          var hit = !q || a.textContent.toLowerCase().indexOf(q) >= 0 || (sec && sec.textContent.toLowerCase().indexOf(q) >= 0);
          a.hidden = !hit;
        });
        toc.querySelectorAll(".group").forEach(function (g) {
          g.hidden = !g.querySelector("a:not([hidden])");
        });
      });
      document.addEventListener("keydown", function (e) {
        if (e.key === "/" && document.activeElement !== filter && !/input|textarea/i.test(document.activeElement.tagName)) {
          e.preventDefault(); filter.focus();
        }
      });
    }
    if ("IntersectionObserver" in window) {
      var current;
      var io = new IntersectionObserver(function (entries) {
        entries.forEach(function (en) {
          if (!en.isIntersecting) return;
          var a = toc.querySelector('a[href="#' + en.target.id + '"]');
          if (!a) return;
          if (current) current.removeAttribute("aria-current");
          a.setAttribute("aria-current", "true"); current = a;
        });
      }, { rootMargin: "-10% 0px -80% 0px" });
      links.forEach(function (a) { var s = document.getElementById(a.hash.slice(1)); if (s) io.observe(s); });
    }
  }

  // Guide: copy button on every code block.
  document.querySelectorAll(".guide main pre").forEach(function (pre) {
    var b = document.createElement("button");
    b.type = "button"; b.className = "pre-copy"; b.textContent = "Copy";
    b.addEventListener("click", function () {
      var t = pre.querySelector("code").textContent;
      if (navigator.clipboard) navigator.clipboard.writeText(t).then(function () {
        b.textContent = "Copied"; setTimeout(function () { b.textContent = "Copy"; }, 1600);
      }, function () {});
    });
    pre.appendChild(b);
  });
})();
