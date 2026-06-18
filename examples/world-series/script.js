/* SPDX-License-Identifier: FSL-1.1-Apache-2.0 */
/* 2025 World Series spray chart. All hit data below is ILLUSTRATIVE sample
   data generated for demonstration — it is NOT official MLB data, and the
   player names are fictional. */
(function () {
  "use strict";

  var SVGNS = "http://www.w3.org/2000/svg";
  var prefersReduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---------- Field geometry (SVG units, viewBox 1000 x 920) ----------
     Home plate sits at the bottom-center. The diamond opens upward.
     Foul lines run from home to the outfield corners at 45 deg. */
  var HOME = { x: 500, y: 820 };
  var BASE_DIST = 150;            // home->1st along the screen diagonal
  var FOUL_LEN = 470;             // length of each foul line on screen
  var WALL_R = 540;               // outfield wall radius from home (screen units)

  // Bases (rotated diamond): 1st to the right, 3rd to the left, 2nd up.
  var B1 = { x: HOME.x + BASE_DIST, y: HOME.y - BASE_DIST };
  var B3 = { x: HOME.x - BASE_DIST, y: HOME.y - BASE_DIST };
  var B2 = { x: HOME.x, y: HOME.y - 2 * BASE_DIST };
  var MOUND = { x: HOME.x, y: HOME.y - BASE_DIST };

  /* Map a hit's polar (angleDeg, distFt) to an SVG point.
     angle 0 = straight up center field; -45 = left-field line; +45 = right-field line.
     distFt is a believable batted-ball distance in feet (~ up to 430). */
  function plot(angleDeg, distFt) {
    var maxFt = 430;
    var r = Math.min(distFt / maxFt, 1.04) * (WALL_R - 14);
    var rad = (angleDeg - 90) * Math.PI / 180; // -90 so 0deg points up
    return { x: HOME.x + r * Math.cos(rad), y: HOME.y + r * Math.sin(rad) };
  }

  /* ---------- Hit type styling ---------- */
  var TYPES = [
    { key: "1B", label: "Single",    color: "#51d6a8", r: 7,  shape: "circle" },
    { key: "2B", label: "Double",    color: "#ffd23f", r: 7.5, shape: "circle" },
    { key: "3B", label: "Triple",    color: "#ff8a3d", r: 8,  shape: "diamond" },
    { key: "HR", label: "Home Run",  color: "#ff4d6d", r: 9,  shape: "star" },
    { key: "OUT", label: "Out",      color: "#6f8a96", r: 5.5, shape: "x" }
  ];
  var TYPE_MAP = {};
  TYPES.forEach(function (t) { TYPE_MAP[t.key] = t; });

  var TEAMS = {
    HARBOR: { abbr: "HAR", name: "Harbor City Mariners", color: "#38bdf8" },
    SUMMIT: { abbr: "SUM", name: "Summit Range Grizzlies", color: "#f4a72c" }
  };

  /* Fictional rosters */
  var ROSTERS = {
    HARBOR: ["M. Castellano", "D. Whitaker", "R. Okafor", "J. Behr", "T. Solano",
             "K. Lindqvist", "A. Marchetti", "P. Nakamura", "E. Vasquez"],
    SUMMIT: ["C. Bellweather", "G. Maddox", "L. Petrov", "S. Ferreira", "B. Okeke",
             "H. Yamada", "N. Calloway", "F. Rossi", "W. Achebe"]
  };

  /* ---------- Seeded RNG for stable illustrative data ---------- */
  function mulberry32(a) {
    return function () {
      a |= 0; a = (a + 0x6D2B79F5) | 0;
      var t = Math.imul(a ^ (a >>> 15), 1 | a);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }
  var rng = mulberry32(20251028);
  function rand(min, max) { return min + (max - min) * rng(); }
  function pick(arr) { return arr[Math.floor(rng() * arr.length)]; }
  function round(n, d) { var p = Math.pow(10, d || 0); return Math.round(n * p) / p; }

  /* ---------- Generate illustrative dataset ----------
     Distances and exit velos are correlated with hit type so the chart
     reads believably (HRs deep + hard, outs/singles shallower). */
  function genHit(id, team, game) {
    var roll = rng();
    var type;
    if (roll < 0.30) type = "OUT";
    else if (roll < 0.62) type = "1B";
    else if (roll < 0.80) type = "2B";
    else if (roll < 0.86) type = "3B";
    else type = "HR";

    var angle, dist, evo;
    switch (type) {
      case "OUT":
        angle = rand(-44, 44);
        // mix of infield popups/grounders and caught fly balls
        dist = rng() < 0.45 ? rand(60, 150) : rand(150, 360);
        evo = round(rand(62, 99), 1);
        break;
      case "1B":
        angle = rand(-43, 43);
        dist = rand(95, 215);
        evo = round(rand(70, 101), 1);
        break;
      case "2B":
        // doubles favor the gaps / down the lines
        angle = rng() < 0.5 ? rand(-44, -22) : rand(22, 44);
        dist = rand(250, 360);
        evo = round(rand(95, 107), 1);
        break;
      case "3B":
        angle = rng() < 0.5 ? rand(-44, -34) : rand(34, 44);
        dist = rand(300, 380);
        evo = round(rand(96, 106), 1);
        break;
      default: // HR
        angle = rand(-40, 40);
        dist = rand(360, 432);
        evo = round(rand(101, 116), 1);
    }

    return {
      id: id,
      team: team,
      game: game,
      batter: pick(ROSTERS[team]),
      inning: Math.floor(rand(1, 10)),
      type: type,
      angle: round(angle, 1),
      dist: Math.round(dist),
      evo: evo
    };
  }

  var HITS = [];
  var idc = 0;
  for (var g = 1; g <= 5; g++) {
    var perTeam = Math.round(rand(9, 13));
    ["HARBOR", "SUMMIT"].forEach(function (team) {
      var n = perTeam + Math.round(rand(-2, 2));
      for (var i = 0; i < n; i++) HITS.push(genHit(++idc, team, g));
    });
  }

  /* ---------- Build the static field SVG ---------- */
  var svg = document.getElementById("field");

  function el(name, attrs) {
    var n = document.createElementNS(SVGNS, name);
    for (var k in attrs) n.setAttribute(k, attrs[k]);
    return n;
  }

  function buildField() {
    var defs = el("defs", {});

    // grass gradient
    var grass = el("linearGradient", { id: "grassGrad", x1: "0", y1: "0", x2: "0", y2: "1" });
    grass.appendChild(el("stop", { offset: "0", "stop-color": "#2a6e45" }));
    grass.appendChild(el("stop", { offset: "1", "stop-color": "#184f31" }));
    defs.appendChild(grass);

    // dirt gradient
    var dirt = el("radialGradient", { id: "dirtGrad", cx: "0.5", cy: "0.62", r: "0.7" });
    dirt.appendChild(el("stop", { offset: "0", "stop-color": "#b06a3f" }));
    dirt.appendChild(el("stop", { offset: "1", "stop-color": "#8a4f2c" }));
    defs.appendChild(dirt);

    // vignette
    var vig = el("radialGradient", { id: "vig", cx: "0.5", cy: "0.78", r: "0.85" });
    vig.appendChild(el("stop", { offset: "0.55", "stop-color": "#000", "stop-opacity": "0" }));
    vig.appendChild(el("stop", { offset: "1", "stop-color": "#000", "stop-opacity": "0.45" }));
    defs.appendChild(vig);

    // soft glow for markers
    var glow = el("filter", { id: "glow", x: "-60%", y: "-60%", width: "220%", height: "220%" });
    glow.appendChild(el("feGaussianBlur", { stdDeviation: "3.2", result: "b" }));
    var mrg = el("feMerge", {});
    mrg.appendChild(el("feMergeNode", { in: "b" }));
    mrg.appendChild(el("feMergeNode", { in: "SourceGraphic" }));
    glow.appendChild(mrg);
    defs.appendChild(glow);

    svg.appendChild(defs);

    // base grass rect
    svg.appendChild(el("rect", { x: "0", y: "0", width: "1000", height: "920", fill: "url(#grassGrad)" }));

    // mowing stripes (alternating subtle bands across the outfield)
    var stripes = el("g", { opacity: "0.5" });
    for (var s = 0; s < 9; s++) {
      var y0 = 40 + s * 78;
      stripes.appendChild(el("rect", {
        x: "0", y: String(y0), width: "1000", height: "39",
        fill: s % 2 === 0 ? "#2f7a4d" : "#216240"
      }));
    }
    svg.appendChild(stripes);

    // outfield wall arc + warning track
    var wallStart = plot(-45, 460);
    var wallEnd = plot(45, 460);
    var trackStart = plot(-45, 425);
    var trackEnd = plot(45, 425);
    // warning track band (dirt-toned)
    svg.appendChild(el("path", {
      d: "M " + trackStart.x + " " + trackStart.y +
         " A " + WALL_R + " " + WALL_R + " 0 0 1 " + trackEnd.x + " " + trackEnd.y +
         " L " + wallEnd.x + " " + wallEnd.y +
         " A " + (WALL_R + 36) + " " + (WALL_R + 36) + " 0 0 0 " + wallStart.x + " " + wallStart.y + " Z",
      fill: "#7a4a2e", opacity: "0.55"
    }));
    // wall line
    svg.appendChild(el("path", {
      d: "M " + wallStart.x + " " + wallStart.y +
         " A " + (WALL_R + 36) + " " + (WALL_R + 36) + " 0 0 1 " + wallEnd.x + " " + wallEnd.y,
      fill: "none", stroke: "#dfe9e6", "stroke-width": "4", opacity: "0.85"
    }));

    // infield dirt (skinned diamond) — a rounded square around the bases
    var inDirt = el("path", {
      d: "M " + HOME.x + " " + (HOME.y + 14) +
         " L " + (B1.x + 26) + " " + B1.y +
         " L " + B2.x + " " + (B2.y - 26) +
         " L " + (B3.x - 26) + " " + B3.y + " Z",
      fill: "url(#dirtGrad)"
    });
    svg.appendChild(inDirt);

    // infield grass cutout (smaller diamond inside the dirt)
    var inset = 36;
    svg.appendChild(el("path", {
      d: "M " + HOME.x + " " + (MOUND.y + 30) +
         " L " + (B1.x - inset) + " " + B1.y +
         " L " + B2.x + " " + (B2.y + inset) +
         " L " + (B3.x + inset) + " " + B3.y + " Z",
      fill: "url(#grassGrad)"
    }));

    // foul lines
    var foulL = plot(-45, FOUL_LEN / (WALL_R - 14) * 430);
    var foulR = plot(45, FOUL_LEN / (WALL_R - 14) * 430);
    [foulL, foulR].forEach(function (p) {
      svg.appendChild(el("line", {
        x1: HOME.x, y1: HOME.y, x2: p.x, y2: p.y,
        stroke: "#f2f7f5", "stroke-width": "3.5", opacity: "0.9"
      }));
    });

    // basepath lines
    var bp = el("path", {
      d: "M " + HOME.x + " " + HOME.y + " L " + B1.x + " " + B1.y +
         " L " + B2.x + " " + B2.y + " L " + B3.x + " " + B3.y + " Z",
      fill: "none", stroke: "#caa", "stroke-width": "2", opacity: "0.35"
    });
    svg.appendChild(bp);

    // pitcher's mound
    svg.appendChild(el("circle", { cx: MOUND.x, cy: MOUND.y, r: "20", fill: "#a05f38" }));
    svg.appendChild(el("rect", { x: String(MOUND.x - 5), y: String(MOUND.y - 2), width: "10", height: "4", fill: "#e7eeec", opacity: "0.8" }));

    // bases
    [B1, B2, B3].forEach(function (b) {
      svg.appendChild(el("rect", {
        x: String(b.x - 8), y: String(b.y - 8), width: "16", height: "16",
        fill: "#f4f8f6", transform: "rotate(45 " + b.x + " " + b.y + ")",
        stroke: "#cdd6d3", "stroke-width": "1"
      }));
    });
    // home plate
    svg.appendChild(el("path", {
      d: "M " + (HOME.x - 9) + " " + (HOME.y - 2) +
         " L " + (HOME.x + 9) + " " + (HOME.y - 2) +
         " L " + (HOME.x + 9) + " " + (HOME.y + 6) +
         " L " + HOME.x + " " + (HOME.y + 13) +
         " L " + (HOME.x - 9) + " " + (HOME.y + 6) + " Z",
      fill: "#f4f8f6"
    }));

    // vignette on top of field, below markers
    svg.appendChild(el("rect", { x: "0", y: "0", width: "1000", height: "920", fill: "url(#vig)" }));

    // marker layer (kept last so hits draw on top)
    var layer = el("g", { id: "marker-layer" });
    svg.appendChild(layer);
    return layer;
  }

  /* ---------- Marker shapes ---------- */
  function makeShape(type, p) {
    var t = TYPE_MAP[type];
    var r = t.r;
    var node;
    if (t.shape === "circle") {
      node = el("circle", { cx: p.x, cy: p.y, r: String(r) });
    } else if (t.shape === "diamond") {
      node = el("path", {
        d: "M " + p.x + " " + (p.y - r) + " L " + (p.x + r) + " " + p.y +
           " L " + p.x + " " + (p.y + r) + " L " + (p.x - r) + " " + p.y + " Z"
      });
    } else if (t.shape === "star") {
      node = el("path", { d: starPath(p.x, p.y, r + 1.5, (r + 1.5) * 0.46, 5) });
    } else { // x
      node = el("path", {
        d: "M " + (p.x - r) + " " + (p.y - r) + " L " + (p.x + r) + " " + (p.y + r) +
           " M " + (p.x + r) + " " + (p.y - r) + " L " + (p.x - r) + " " + (p.y + r),
        fill: "none", stroke: t.color, "stroke-width": "3", "stroke-linecap": "round"
      });
      return node;
    }
    node.setAttribute("fill", t.color);
    node.setAttribute("stroke", "rgba(4,12,16,0.65)");
    node.setAttribute("stroke-width", "1.2");
    return node;
  }

  function starPath(cx, cy, rOut, rIn, points) {
    var d = "", step = Math.PI / points;
    for (var i = 0; i < 2 * points; i++) {
      var rr = (i % 2 === 0) ? rOut : rIn;
      var a = i * step - Math.PI / 2;
      d += (i === 0 ? "M " : "L ") + (cx + rr * Math.cos(a)) + " " + (cy + rr * Math.sin(a)) + " ";
    }
    return d + "Z";
  }

  /* ---------- State ---------- */
  var state = {
    team: "all",
    game: "all",
    types: { "1B": true, "2B": true, "3B": true, "HR": true, "OUT": true }
  };

  function visible() {
    return HITS.filter(function (h) {
      if (state.team !== "all" && h.team !== state.team) return false;
      if (state.game !== "all" && String(h.game) !== state.game) return false;
      if (!state.types[h.type]) return false;
      return true;
    });
  }

  /* ---------- Render markers ---------- */
  var layer = buildField();
  var tooltip = document.getElementById("tooltip");
  var fieldFrame = document.querySelector(".field-frame");

  function render(animate) {
    while (layer.firstChild) layer.removeChild(layer.firstChild);
    var hits = visible();
    hits.forEach(function (h, i) {
      var p = plot(h.angle, h.dist);
      var g = el("g", {
        class: "marker" + (animate && !prefersReduced ? " marker--enter" : ""),
        tabindex: "0", role: "button",
        "aria-label": h.batter + ", " + TYPE_MAP[h.type].label + ", inning " + h.inning +
          ", " + h.evo + " mph exit velo, " + h.dist + " feet"
      });
      if (animate && !prefersReduced) g.style.animationDelay = Math.min(i * 7, 380) + "ms";
      g.appendChild(makeShape(h.type, p));
      // larger invisible hit target for easy hover/tap
      var hit = el("circle", { cx: p.x, cy: p.y, r: "14", fill: "transparent" });
      g.appendChild(hit);

      g.addEventListener("mouseenter", function () { showTip(h, p); });
      g.addEventListener("mousemove", function () { showTip(h, p); });
      g.addEventListener("mouseleave", hideTip);
      g.addEventListener("focus", function () { showTip(h, p); });
      g.addEventListener("blur", hideTip);
      layer.appendChild(g);
    });
    updateCount(hits.length);
    updateStats(hits);
  }

  /* ---------- Tooltip ---------- */
  function showTip(h, p) {
    var team = TEAMS[h.team];
    var t = TYPE_MAP[h.type];
    tooltip.innerHTML =
      '<div class="tooltip__name">' + esc(h.batter) +
        '<span class="tooltip__team" style="background:' + team.color + '">' + team.abbr + '</span></div>' +
      '<div class="tooltip__type" style="color:' + t.color + '">' + t.label + '</div>' +
      '<dl class="tooltip__rows">' +
        '<dt>Inning</dt><dd>' + h.inning + '</dd>' +
        '<dt>Game</dt><dd>' + h.game + '</dd>' +
        '<dt>Exit velo</dt><dd>' + h.evo.toFixed(1) + ' mph</dd>' +
        '<dt>Distance</dt><dd>' + h.dist + ' ft</dd>' +
      '</dl>';
    // position tooltip relative to the frame using the marker's fractional location
    var fx = (p.x / 1000) * 100;
    var fy = (p.y / 920) * 100;
    tooltip.style.left = fx + "%";
    tooltip.style.top = fy + "%";
    tooltip.hidden = false;
  }
  function hideTip() { tooltip.hidden = true; }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) {
    return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
  }); }

  /* ---------- Count line ---------- */
  function updateCount(n) {
    var el2 = document.getElementById("field-count");
    el2.innerHTML = "Showing <b>" + n + "</b> of " + HITS.length + " batted balls";
  }

  /* ---------- Stats panel ---------- */
  function updateStats(hits) {
    document.getElementById("stat-total").textContent = hits.length;

    // by type
    var byType = {};
    TYPES.forEach(function (t) { byType[t.key] = 0; });
    hits.forEach(function (h) { byType[h.type]++; });
    var maxType = Math.max(1, Math.max.apply(null, TYPES.map(function (t) { return byType[t.key]; })));
    var grid = document.getElementById("stat-types");
    grid.innerHTML = "";
    TYPES.forEach(function (t) {
      var row = document.createElement("div");
      row.className = "stat-row";
      row.style.color = t.color;
      row.innerHTML =
        '<span class="dot" style="background:' + t.color + '"></span>' +
        '<dt style="color:var(--ink-dim)">' + t.label + '</dt>' +
        '<dd style="color:var(--ink)">' + byType[t.key] + '</dd>' +
        '<span class="bar" style="width:' + (byType[t.key] / maxType * 100) + '%"></span>';
      grid.appendChild(row);
    });

    // by team split
    var har = hits.filter(function (h) { return h.team === "HARBOR"; }).length;
    var sum = hits.length - har;
    var total = Math.max(1, hits.length);
    var split = document.getElementById("stat-teams");
    split.innerHTML =
      '<div class="stat-split__bar">' +
        '<div class="stat-split__seg" style="width:' + (har / total * 100) + '%;background:' + TEAMS.HARBOR.color + '">' + (har || "") + '</div>' +
        '<div class="stat-split__seg" style="width:' + (sum / total * 100) + '%;background:' + TEAMS.SUMMIT.color + '">' + (sum || "") + '</div>' +
      '</div>' +
      '<div class="stat-split__legend">' +
        '<span>' + TEAMS.HARBOR.abbr + ' <b>' + har + '</b></span>' +
        '<span><b>' + sum + '</b> ' + TEAMS.SUMMIT.abbr + '</span>' +
      '</div>';

    // extras
    var evoEl = document.getElementById("stat-evo");
    var hrEl = document.getElementById("stat-hr");
    var hitsEl = document.getElementById("stat-hits");
    if (hits.length) {
      var avg = hits.reduce(function (a, h) { return a + h.evo; }, 0) / hits.length;
      evoEl.textContent = avg.toFixed(1) + " mph";
    } else {
      evoEl.textContent = "—";
    }
    var hrs = hits.filter(function (h) { return h.type === "HR"; });
    if (hrs.length) {
      var longest = hrs.reduce(function (a, h) { return h.dist > a.dist ? h : a; });
      hrEl.textContent = longest.dist + " ft";
    } else {
      hrEl.textContent = "—";
    }
    var base = byType["1B"] + byType["2B"] + byType["3B"] + byType["HR"];
    hitsEl.textContent = String(base);
  }

  /* ---------- Legend + toggles ---------- */
  function buildLegend() {
    var legend = document.getElementById("legend");
    TYPES.forEach(function (t) {
      var li = document.createElement("li");
      var sw = document.createElement("span");
      sw.className = "swatch";
      sw.innerHTML = swatchSVG(t);
      li.appendChild(sw);
      li.appendChild(document.createTextNode(t.label));
      legend.appendChild(li);
    });
  }
  function swatchSVG(t) {
    // mini inline SVG matching the marker shape
    var inner;
    if (t.shape === "circle") inner = '<circle cx="7" cy="7" r="6" fill="' + t.color + '"/>';
    else if (t.shape === "diamond") inner = '<path d="M7 1 L13 7 L7 13 L1 7 Z" fill="' + t.color + '"/>';
    else if (t.shape === "star") inner = '<path d="' + starPath(7, 7, 6.5, 3, 5) + '" fill="' + t.color + '"/>';
    else inner = '<path d="M2 2 L12 12 M12 2 L2 12" stroke="' + t.color + '" stroke-width="2.5" stroke-linecap="round"/>';
    return '<svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">' + inner + '</svg>';
  }

  function buildToggles() {
    var wrap = document.getElementById("type-toggles");
    TYPES.forEach(function (t) {
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "toggle";
      btn.setAttribute("aria-pressed", "true");
      btn.dataset.type = t.key;
      btn.innerHTML =
        '<span class="toggle__swatch" style="background:' + t.color + '"></span>' +
        '<span>' + t.label + '</span>' +
        '<span class="toggle__count" data-count="' + t.key + '"></span>';
      btn.addEventListener("click", function () {
        state.types[t.key] = !state.types[t.key];
        btn.setAttribute("aria-pressed", String(state.types[t.key]));
        render(true);
      });
      wrap.appendChild(btn);
    });
    refreshToggleCounts();
  }

  // Toggle counts reflect totals under the current team/game scope (ignoring type filters).
  function refreshToggleCounts() {
    var scope = HITS.filter(function (h) {
      if (state.team !== "all" && h.team !== state.team) return false;
      if (state.game !== "all" && String(h.game) !== state.game) return false;
      return true;
    });
    var counts = {};
    TYPES.forEach(function (t) { counts[t.key] = 0; });
    scope.forEach(function (h) { counts[h.type]++; });
    TYPES.forEach(function (t) {
      var c = document.querySelector('.toggle__count[data-count="' + t.key + '"]');
      if (c) c.textContent = counts[t.key];
    });
  }

  /* ---------- Wire controls ---------- */
  document.getElementById("team-select").addEventListener("change", function (e) {
    state.team = e.target.value;
    refreshToggleCounts();
    render(true);
  });
  document.getElementById("game-select").addEventListener("change", function (e) {
    state.game = e.target.value;
    refreshToggleCounts();
    render(true);
  });
  document.getElementById("reset-btn").addEventListener("click", function () {
    state.team = "all";
    state.game = "all";
    TYPES.forEach(function (t) { state.types[t.key] = true; });
    document.getElementById("team-select").value = "all";
    document.getElementById("game-select").value = "all";
    document.querySelectorAll(".toggle").forEach(function (b) { b.setAttribute("aria-pressed", "true"); });
    refreshToggleCounts();
    render(true);
  });

  // hide tooltip when scrolling/leaving the field
  fieldFrame.addEventListener("mouseleave", hideTip);

  /* ---------- Init ---------- */
  buildLegend();
  buildToggles();
  render(true);
})();
