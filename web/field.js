// The hero field: a handful of ants (the agents) walk between the steps of
// a flow. Each one drops scent marks as it goes and leans toward marks
// already there, so shared paths emerge and unused ones fade. An ant
// carries its result to the next step; after the human step it carries
// the human's answer.
(function () {
  "use strict";
  var canvas = document.getElementById("field");
  if (!canvas || !canvas.getContext) return;
  var ctx = canvas.getContext("2d");
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)");

  var NODES = [
    { id: "start", x: 0.50, y: 0.20 },
    { id: "plan", x: 0.66, y: 0.12 },
    { id: "research", x: 0.87, y: 0.20 },
    { id: "research", x: 0.94, y: 0.44 },
    { id: "research", x: 0.80, y: 0.38 },
    { id: "review", x: 0.70, y: 0.60 },
    { id: "human", x: 0.91, y: 0.72 },
    { id: "write", x: 0.57, y: 0.82 },
  ];
  var EDGES = { 0: [1], 1: [2, 3, 4], 2: [5], 3: [5], 4: [5], 5: [7, 7, 6], 6: [5], 7: [0] };
  var START = 0, HUMAN = 6;

  // Invisible steering field, read by the ants.
  var CELL = 6, gw, gh, grid, next;
  // Visible scent layer.
  var trail, tctx;
  var W, H, dpr, ants, nodes, colors, raf, frame = 0, scale = 1;

  function readColors() {
    var s = getComputedStyle(document.documentElement);
    function v(n) { return s.getPropertyValue(n).trim(); }
    function lum(c) {
      var n = parseInt(c.replace("#", ""), 16);
      return ((n >> 16 & 255) * 0.299 + (n >> 8 & 255) * 0.587 + (n & 255) * 0.114) / 255;
    }
    function rgb(c) {
      if (c[0] === "#") { var n = parseInt(c.slice(1), 16); return (n >> 16 & 255) + "," + (n >> 8 & 255) + "," + (n & 255); }
      var m = c.match(/\d+/g); return m ? m.slice(0, 3).join(",") : "0,0,0";
    }
    colors = { rule: v("--rule"), trail: v("--trail"), trailRGB: rgb(v("--trail")), ink: v("--ink"), ink3: v("--ink-3"),
      chalk: v("--chalk"), dark: lum(v("--chalk")) < 0.5, pollen: v("--pollen"), pollenRGB: rgb(v("--pollen")), font: v("--display") };
  }

  function layout() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    var r = canvas.getBoundingClientRect();
    W = r.width; H = r.height;
    canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
    trail = document.createElement("canvas"); trail.width = canvas.width; trail.height = canvas.height;
    tctx = trail.getContext("2d"); tctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    gw = Math.ceil(W / CELL); gh = Math.ceil(H / CELL);
    grid = new Float32Array(gw * gh); next = new Float32Array(gw * gh);
    var narrow = W < 860;
    scale = narrow ? 0.95 : 1.25;
    nodes = NODES.map(function (n) {
      var x = narrow ? 0.06 + (n.x - 0.5) * 1.6 : n.x, y = narrow ? (90 + n.y * 300) / H : 0.1 + n.y * 0.78;
      return { id: n.id, x: x * W, y: y * H, pulses: [], busy: 0 };
    });
    var count = narrow ? 7 : 10;
    ants = [];
    for (var i = 0; i < count; i++) {
      // spread the ants along the flow's edges, each partway to its next step
      var from = i % nodes.length, to = pick(from), f = 0.15 + ((i * 0.37) % 0.7);
      var A = nodes[from], B = nodes[to], a = Math.atan2(B.y - A.y, B.x - A.x);
      ants.push({ x: A.x + (B.x - A.x) * f, y: A.y + (B.y - A.y) * f, a: a, ra: a, to: to, at: from,
        wait: 0, gait: Math.random() * 6.28, hue: Math.random() * 360, mark: 0, carry: from === HUMAN ? "answer" : from === START ? null : "work" });
    }
  }

  function pick(from) { var e = EDGES[from]; return e[Math.floor(Math.random() * e.length)]; }
  function sense(x, y) {
    var gx = x / CELL | 0, gy = y / CELL | 0;
    if (gx < 0 || gy < 0 || gx >= gw || gy >= gh) return 0;
    return grid[gy * gw + gx];
  }

  function step(draw) {
    var SA = 0.45, SD = 22, SPEED = 0.95;
    for (var z = 0; z < nodes.length; z++) nodes[z].busy = 0;
    for (var i = 0; i < ants.length; i++) {
      var p = ants[i];
      if (p.wait > 0) {
        p.wait--; nodes[p.at].busy++;
        if (p.wait === 0) p.carry = p.at === HUMAN ? "answer" : p.at === START ? null : "work";
        continue;
      }
      var t = nodes[p.to], dx = t.x - p.x, dy = t.y - p.y, d = Math.hypot(dx, dy);
      if (d < 17) {
        p.at = p.to; p.carry = null;
        p.wait = p.to === HUMAN ? 160 + Math.random() * 120 | 0 : 40 + Math.random() * 40 | 0;
        if (draw) t.pulses.push(0);
        p.to = pick(p.to);
        continue;
      }
      var goal = Math.atan2(dy, dx);
      var diff = Math.atan2(Math.sin(goal - p.a), Math.cos(goal - p.a));
      if (d > 60 && Math.abs(diff) < 0.9) {
        var l = sense(p.x + Math.cos(p.a - SA) * SD, p.y + Math.sin(p.a - SA) * SD);
        var r = sense(p.x + Math.cos(p.a + SA) * SD, p.y + Math.sin(p.a + SA) * SD);
        if (Math.abs(l - r) > 0.02) p.a += l > r ? -0.03 : 0.03;
      }
      p.a += diff * (d < 60 ? 0.12 : 0.055) + (Math.random() - .5) * 0.05;
      // keep a distance: slow down behind another ant, step aside from one alongside
      var speed = SPEED, hx = Math.cos(p.a), hy = Math.sin(p.a);
      for (var o = 0; o < ants.length; o++) {
        var q = ants[o];
        if (q === p) continue;
        var ox = q.x - p.x, oy = q.y - p.y, od = Math.hypot(ox, oy);
        if (od > 30 || od < 0.01) continue;
        var ahead = (ox * hx + oy * hy) / od;
        var same = Math.cos(q.a - p.a) > 0.2 && q.wait === 0;
        if (same && ahead > 0.5 && od < 26) speed = Math.min(speed, SPEED * Math.max(0, (od - 14) / 12));
        if (od < 18 || (!same && ahead > 0.3 && od < 28)) p.a += (ox * hy - oy * hx) > 0 ? -0.07 : 0.07;
      }
      p.x += Math.cos(p.a) * speed; p.y += Math.sin(p.a) * speed;
      if (speed < 0.05) continue;
      p.gait += 0.34 * speed / SPEED;
      // the drawn heading follows the walking heading smoothly
      p.ra += Math.atan2(Math.sin(p.a - p.ra), Math.cos(p.a - p.ra)) * 0.2;
      var gx = p.x / CELL | 0, gy = p.y / CELL | 0;
      if (gx >= 0 && gy >= 0 && gx < gw && gy < gh) { var k = gy * gw + gx; grid[k] = Math.min(1, grid[k] + 0.5); }
      if (draw && ++p.mark % 7 === 0) {
        tctx.fillStyle = "rgba(" + (p.carry === "answer" ? colors.pollenRGB : colors.trailRGB) + ",0.75)";
        tctx.beginPath(); tctx.arc(p.x, p.y, 1.4, 0, 6.2832); tctx.fill();
      }
    }
    if (frame % 2) return;
    for (var y = 1; y < gh - 1; y++) {
      for (var x = 1; x < gw - 1; x++) {
        var j = y * gw + x;
        next[j] = ((grid[j] * 12 + grid[j - 1] + grid[j + 1] + grid[j - gw] + grid[j + gw]) / 16) * 0.996;
      }
    }
    var tmp = grid; grid = next; next = tmp;
  }

  // Fade the scent layer in larger, rarer steps so 8-bit alpha leaves no haze.
  function fade() {
    if (frame % 16) return;
    tctx.save();
    tctx.setTransform(1, 0, 0, 1, 0, 0);
    tctx.globalCompositeOperation = "destination-out";
    tctx.fillStyle = "rgba(0,0,0,0.1)";
    tctx.fillRect(0, 0, trail.width, trail.height);
    tctx.restore();
  }

  // One ant, nose along +x, about 22 units long.
  function drawAnt(p) {
    var walking = p.wait === 0;
    var ph = walking ? p.gait : 0;
    ctx.save();
    ctx.translate(p.x, p.y);
    ctx.rotate(p.ra);
    ctx.scale(scale, scale);
    // each ant has its own hue, darker on a light page and lighter on a dark one
    var body = "hsl(" + (p.hue | 0) + ", 62%, " + (colors.dark ? "68%" : "40%") + ")";
    ctx.strokeStyle = body; ctx.fillStyle = body;
    ctx.lineCap = "round"; ctx.lineJoin = "round"; ctx.lineWidth = 1.15;

    // legs: tripod gait, front and back of one side swing with the middle of the other
    var LEGS = [[2.2, 4, 3.5, 9, 7.5], [0, 0, 5, 0.5, 9.5], [-2, -3.5, 4.5, -8.5, 8.5]];
    for (var side = -1; side <= 1; side += 2) {
      for (var g = 0; g < 3; g++) {
        var L = LEGS[g], phase = ph + (g === 1 ? Math.PI : 0) + (side > 0 ? Math.PI : 0);
        var sw = walking ? Math.sin(phase) * 2.6 : 0;
        ctx.beginPath();
        ctx.moveTo(L[0], side * 1.2);
        ctx.lineTo(L[1] + sw * 0.6, side * L[2]);
        ctx.lineTo(L[3] + sw, side * L[4]);
        ctx.stroke();
      }
    }
    // antennae
    var aw = Math.sin(frame * 0.09 + p.gait * 0.2) * 0.6;
    ctx.lineWidth = 0.9;
    ctx.beginPath();
    ctx.moveTo(6.6, -1); ctx.lineTo(9.2, -3.2 + aw); ctx.lineTo(12.6, -2.4 + aw);
    ctx.moveTo(6.6, 1); ctx.lineTo(9.2, 3.2 - aw); ctx.lineTo(12.6, 2.4 - aw);
    ctx.stroke();
    // body: abdomen, waist, thorax, head
    ctx.beginPath(); ctx.ellipse(-6.4, 0, 5.2, 3.7, 0, 0, 6.2832); ctx.fill();
    ctx.beginPath(); ctx.ellipse(-1.2, 0, 1.2, 1, 0, 0, 6.2832); ctx.fill();
    ctx.beginPath(); ctx.ellipse(1.6, 0, 2.8, 1.9, 0, 0, 6.2832); ctx.fill();
    ctx.beginPath(); ctx.ellipse(5.6, 0, 2.5, 2.3, 0, 0, 6.2832); ctx.fill();
    // highlight on the abdomen, so the body reads as round
    ctx.fillStyle = "rgba(255,255,255,0.18)";
    ctx.beginPath(); ctx.ellipse(-7.2, -1.3, 2.4, 1.1, -0.2, 0, 6.2832); ctx.fill();
    // what it carries, held in the jaws
    if (p.carry) {
      ctx.fillStyle = p.carry === "answer" ? colors.pollen : colors.trail;
      ctx.strokeStyle = colors.ink; ctx.lineWidth = 0.8;
      ctx.beginPath(); ctx.roundRect ? ctx.roundRect(8, -2.4, 4.8, 4.8, 1.2) : ctx.rect(8, -2.4, 4.8, 4.8);
      ctx.fill(); ctx.stroke();
    }
    ctx.restore();
  }

  function draw() {
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.drawImage(trail, 0, 0);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    // the flow itself, faint, under the trails
    ctx.strokeStyle = colors.rule; ctx.lineWidth = 1;
    ctx.beginPath();
    for (var e in EDGES) {
      EDGES[e].forEach(function (to) {
        var A = nodes[e], B = nodes[to];
        ctx.moveTo(A.x, A.y); ctx.lineTo(B.x, B.y);
      });
    }
    ctx.stroke();

    ctx.font = "500 13px " + colors.font;
    ctx.textBaseline = "middle";
    for (var m = 0; m < nodes.length; m++) {
      var n = nodes[m], human = m === HUMAN, rgb = human ? colors.pollenRGB : colors.trailRGB;
      for (var q = n.pulses.length - 1; q >= 0; q--) {
        var age = n.pulses[q]++;
        if (age > 80) { n.pulses.splice(q, 1); continue; }
        var k = age / 80;
        ctx.strokeStyle = "rgba(" + rgb + "," + (0.6 * (1 - k) * (1 - k)) + ")";
        ctx.lineWidth = 1.2;
        ctx.beginPath(); ctx.arc(n.x, n.y, 12 + k * 26, 0, 6.2832); ctx.stroke();
      }
      // a step is a ring; it fills while an ant works there
      ctx.beginPath(); ctx.arc(n.x, n.y, 12, 0, 6.2832);
      ctx.fillStyle = n.busy ? "rgba(" + rgb + "," + (human ? 0.45 : 0.16) + ")" : colors.chalk;
      ctx.fill();
      ctx.lineWidth = 1.5; ctx.strokeStyle = human ? colors.pollen : colors.ink; ctx.stroke();
    }
    for (var i = 0; i < ants.length; i++) drawAnt(ants[i]);
    // labels last, so an ant never hides a step's name
    ctx.lineJoin = "round";
    for (var w = 0; w < nodes.length; w++) {
      var nn = nodes[w];
      ctx.lineWidth = 5; ctx.strokeStyle = colors.chalk;
      ctx.strokeText(nn.id, nn.x + 19, nn.y + 1);
      ctx.fillStyle = colors.ink3;
      ctx.fillText(nn.id, nn.x + 19, nn.y + 1);
    }
  }

  function loop() {
    frame++;
    fade(); step(true); draw();
    raf = requestAnimationFrame(loop);
  }

  function start() {
    cancelAnimationFrame(raf);
    readColors(); layout();
    // walk for a while first, so the page opens on trails already laid
    for (var i = 0; i < 900; i++) { frame++; fade(); step(i > 300); }
    nodes.forEach(function (n) { n.pulses = []; });
    if (reduce.matches) { draw(); return; }
    loop();
  }

  var rt;
  window.addEventListener("resize", function () { clearTimeout(rt); rt = setTimeout(start, 180); });
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () { readColors(); if (reduce.matches) draw(); });
  reduce.addEventListener("change", start);
  document.addEventListener("visibilitychange", function () {
    cancelAnimationFrame(raf);
    if (!document.hidden && !reduce.matches) loop();
  });
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(start); else start();
})();
