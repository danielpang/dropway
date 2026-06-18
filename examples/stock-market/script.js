/* SPDX-License-Identifier: FSL-1.1-Apache-2.0 */
/* Helix Terminal — synthetic market engine + hand-drawn canvas charts.
   ALL DATA IS SIMULATED. Random-walk price model with drift & volatility. */
'use strict';

(() => {
  const REDUCED = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* ---------------------------------------------------------------------- *
   * Deterministic-ish RNG helpers + synthetic price model                  *
   * ---------------------------------------------------------------------- */
  const rnd = (a, b) => a + Math.random() * (b - a);
  // Box–Muller standard normal for realistic returns
  function gauss() {
    let u = 0, v = 0;
    while (u === 0) u = Math.random();
    while (v === 0) v = Math.random();
    return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v);
  }

  // One candle step from a random walk with drift + volatility.
  function stepCandle(prevClose, vol, drift) {
    const open = prevClose;
    // intrabar path of small ticks -> realistic high/low/close
    let p = open, hi = open, lo = open;
    const ticks = 14;
    for (let i = 0; i < ticks; i++) {
      p *= 1 + (drift / ticks) + (vol / Math.sqrt(ticks)) * gauss();
      if (p > hi) hi = p;
      if (p < lo) lo = p;
    }
    const close = p;
    const volume = Math.max(1, (0.6 + Math.abs(close - open) / (open * vol + 1e-9)) * rnd(0.8, 1.4));
    return { open, high: hi, low: lo, close, volume };
  }

  function seedSeries(n, start, vol, drift) {
    const out = [];
    let prev = start;
    for (let i = 0; i < n; i++) {
      const c = stepCandle(prev, vol, drift);
      out.push(c);
      prev = c.close;
    }
    return out;
  }

  // simple moving average over closes
  function sma(candles, period) {
    const out = new Array(candles.length).fill(null);
    let sum = 0;
    for (let i = 0; i < candles.length; i++) {
      sum += candles[i].close;
      if (i >= period) sum -= candles[i - period].close;
      if (i >= period - 1) out[i] = sum / period;
    }
    return out;
  }

  const fmt = (v, d = 2) => v.toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d });
  const fmtVol = (v) => v >= 1e6 ? (v / 1e6).toFixed(2) + 'M' : v >= 1e3 ? (v / 1e3).toFixed(1) + 'K' : v.toFixed(0);

  /* ---------------------------------------------------------------------- *
   * 1. HERO CANDLESTICK CHART                                              *
   * ---------------------------------------------------------------------- */
  const COLORS = {
    up: getCSS('--up'), down: getCSS('--down'),
    grid: getCSS('--grid'), ink: getCSS('--ink-dim'),
    faint: getCSS('--ink-faint'), ma: getCSS('--gold'), accent: getCSS('--accent'),
  };
  function getCSS(v) { return getComputedStyle(document.documentElement).getPropertyValue(v).trim(); }

  const TF = { '1D': { n: 90, vol: 0.006, drift: 0.0006 }, '1W': { n: 90, vol: 0.012, drift: 0.0009 }, '1M': { n: 90, vol: 0.02, drift: 0.0012 } };

  const heroState = {
    tf: '1D',
    candles: seedSeries(90, 182.4, TF['1D'].vol, TF['1D'].drift),
    showMA: true,
    hoverX: null,
    // smooth-scroll animation: fractional offset of newest forming candle
    formT: 0,
    forming: null,
  };

  const canvas = document.getElementById('heroChart');
  const ctx = canvas.getContext('2d');
  const readout = document.getElementById('readout');
  let CW = 0, CH = 0, DPR = 1;
  // plot geometry (filled each frame)
  let geo = { x0: 0, x1: 0, y0: 0, y1: 0, min: 0, max: 1, cw: 6, gap: 2 };

  function resize() {
    DPR = Math.min(window.devicePixelRatio || 1, 2);
    const r = canvas.getBoundingClientRect();
    CW = r.width; CH = r.height;
    canvas.width = Math.round(CW * DPR);
    canvas.height = Math.round(CH * DPR);
    ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
  }
  window.addEventListener('resize', resize);

  function priceToY(p) {
    const { y0, y1, min, max } = geo;
    return y1 - ((p - min) / (max - min)) * (y1 - y0);
  }

  function drawHero() {
    const padL = 8, padR = 64, padT = 14, padB = 26;
    geo.x0 = padL; geo.x1 = CW - padR;
    geo.y0 = padT; geo.y1 = CH - padB;

    const candles = heroState.candles;
    const visible = candles.length;
    const ma = heroState.showMA ? sma(candles, 20) : null;

    // price range across visible candles (+ forming)
    let min = Infinity, max = -Infinity;
    for (const c of candles) { if (c.low < min) min = c.low; if (c.high > max) max = c.high; }
    if (heroState.forming) { min = Math.min(min, heroState.forming.low); max = Math.max(max, heroState.forming.high); }
    const pad = (max - min) * 0.08 || 1;
    min -= pad; max += pad;
    geo.min = min; geo.max = max;

    const plotW = geo.x1 - geo.x0;
    const slot = plotW / (visible + 1); // +1 leaves room for the forming candle on the right
    const cw = Math.max(2, Math.min(10, slot * 0.62));
    geo.cw = cw; geo.gap = slot;
    geo.slot = slot;

    ctx.clearRect(0, 0, CW, CH);

    // ----- horizontal gridlines + price axis -----
    ctx.lineWidth = 1;
    ctx.font = '10px "Chivo Mono", monospace';
    ctx.textBaseline = 'middle';
    const rows = 5;
    for (let i = 0; i <= rows; i++) {
      const p = min + (max - min) * (i / rows);
      const y = priceToY(p);
      ctx.strokeStyle = COLORS.grid;
      ctx.beginPath(); ctx.moveTo(geo.x0, y + 0.5); ctx.lineTo(geo.x1, y + 0.5); ctx.stroke();
      ctx.fillStyle = COLORS.faint;
      ctx.textAlign = 'left';
      ctx.fillText(fmt(p), geo.x1 + 7, y);
    }
    // vertical faint guides
    for (let i = 0; i <= 6; i++) {
      const x = geo.x0 + (plotW * i / 6);
      ctx.strokeStyle = 'rgba(120,150,210,0.035)';
      ctx.beginPath(); ctx.moveTo(x + 0.5, geo.y0); ctx.lineTo(x + 0.5, geo.y1); ctx.stroke();
    }

    // smooth horizontal scroll: translate everything left by fractional slot
    const scrollX = -heroState.formT * slot;
    ctx.save();
    ctx.beginPath();
    ctx.rect(geo.x0 - 2, geo.y0 - 4, plotW + 6, geo.y1 - geo.y0 + 8);
    ctx.clip();

    // ----- candles -----
    const xOf = (i) => geo.x0 + slot * (i + 0.5) + scrollX;
    for (let i = 0; i < visible; i++) {
      const c = candles[i];
      drawCandle(c, xOf(i), cw);
    }
    // forming candle (rightmost, grows in)
    if (heroState.forming) drawCandle(heroState.forming, xOf(visible), cw, true);

    // ----- moving average overlay -----
    if (ma) {
      ctx.lineWidth = 1.6;
      ctx.strokeStyle = COLORS.ma;
      ctx.shadowColor = 'rgba(243,201,105,0.5)';
      ctx.shadowBlur = 8;
      ctx.beginPath();
      let started = false;
      for (let i = 0; i < visible; i++) {
        if (ma[i] == null) continue;
        const x = xOf(i), y = priceToY(ma[i]);
        if (!started) { ctx.moveTo(x, y); started = true; } else ctx.lineTo(x, y);
      }
      ctx.stroke();
      ctx.shadowBlur = 0;
    }
    ctx.restore();

    // ----- last-price marker line + label -----
    const last = heroState.forming || candles[candles.length - 1];
    const ly = priceToY(last.close);
    const up = last.close >= last.open;
    ctx.setLineDash([3, 4]);
    ctx.strokeStyle = up ? hexA(COLORS.up, 0.5) : hexA(COLORS.down, 0.5);
    ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(geo.x0, ly + 0.5); ctx.lineTo(geo.x1, ly + 0.5); ctx.stroke();
    ctx.setLineDash([]);
    // price tag
    const tag = fmt(last.close);
    ctx.font = '600 10px "Chivo Mono", monospace';
    const tw = ctx.measureText(tag).width + 10;
    ctx.fillStyle = up ? COLORS.up : COLORS.down;
    roundRect(geo.x1 + 2, ly - 8, tw, 16, 4); ctx.fill();
    ctx.fillStyle = '#05070d'; ctx.textAlign = 'left'; ctx.textBaseline = 'middle';
    ctx.fillText(tag, geo.x1 + 7, ly + 0.5);

    // ----- crosshair -----
    if (heroState.hoverX != null) drawCrosshair(xOf, visible);
  }

  function drawCandle(c, x, cw, forming) {
    const up = c.close >= c.open;
    const col = up ? COLORS.up : COLORS.down;
    const yO = priceToY(c.open), yC = priceToY(c.close);
    const yH = priceToY(c.high), yL = priceToY(c.low);
    // wick
    ctx.strokeStyle = forming ? hexA(col, 0.85) : col;
    ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(x, yH); ctx.lineTo(x, yL); ctx.stroke();
    // body
    const top = Math.min(yO, yC);
    const h = Math.max(1, Math.abs(yC - yO));
    ctx.fillStyle = forming ? hexA(col, 0.7) : col;
    if (up) { ctx.globalAlpha = 0.92; } else { ctx.globalAlpha = 1; }
    ctx.fillRect(x - cw / 2, top, cw, h);
    ctx.globalAlpha = 1;
    if (forming) { // subtle glow on the live candle
      ctx.shadowColor = up ? getCSS('--up-glow') : getCSS('--down-glow');
      ctx.shadowBlur = 10;
      ctx.fillRect(x - cw / 2, top, cw, h);
      ctx.shadowBlur = 0;
    }
  }

  function drawCrosshair(xOf, visible) {
    // nearest candle index to hover
    const slot = geo.slot;
    const scrollX = -heroState.formT * slot;
    let idx = Math.round((heroState.hoverX - geo.x0 - slot * 0.5 - scrollX) / slot);
    idx = Math.max(0, Math.min(visible - 1, idx));
    const c = heroState.candles[idx];
    if (!c) return;
    const cx = xOf(idx);
    const cy = priceToY(c.close);

    ctx.save();
    ctx.setLineDash([2, 3]);
    ctx.strokeStyle = hexA(COLORS.accent, 0.55);
    ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(cx, geo.y0); ctx.lineTo(cx, geo.y1); ctx.stroke();
    ctx.beginPath(); ctx.moveTo(geo.x0, cy); ctx.lineTo(geo.x1, cy); ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = COLORS.accent;
    ctx.beginPath(); ctx.arc(cx, cy, 3, 0, Math.PI * 2); ctx.fill();
    ctx.restore();

    // populate HTML readout
    const up = c.close >= c.open;
    const total = visible;
    const minsAgo = (total - 1 - idx);
    const t = new Date(Date.now() - minsAgo * 60000);
    const hh = String(t.getHours()).padStart(2, '0');
    const mm = String(t.getMinutes()).padStart(2, '0');
    readout.hidden = false;
    readout.innerHTML =
      `<div class="ro__t">${hh}:${mm} · SIM</div>` +
      `<div class="ro__row"><span>O</span><span class="ro__o num">${fmt(c.open)}</span></div>` +
      `<div class="ro__row"><span>H</span><span class="ro__o num">${fmt(c.high)}</span></div>` +
      `<div class="ro__row"><span>L</span><span class="ro__o num">${fmt(c.low)}</span></div>` +
      `<div class="ro__row"><span>C</span><span class="ro__c ${up ? 'up' : 'down'} num">${fmt(c.close)}</span></div>` +
      `<div class="ro__row"><span>Vol</span><span class="ro__o num">${fmtVol(c.volume * 1e5)}</span></div>`;
    // position (clamp inside chart wrap)
    const wrap = canvas.parentElement.getBoundingClientRect();
    let px = cx; let py = Math.min(cy + 14, CH - 130);
    px = Math.max(80, Math.min(CW - 80, px));
    readout.style.left = px + 'px';
    readout.style.top = py + 'px';
  }

  // hover handling
  canvas.addEventListener('pointermove', (e) => {
    const r = canvas.getBoundingClientRect();
    heroState.hoverX = e.clientX - r.left;
    if (REDUCED) drawHero();
  });
  canvas.addEventListener('pointerleave', () => { heroState.hoverX = null; readout.hidden = true; if (REDUCED) drawHero(); });

  // header price elements
  const elLast = document.getElementById('heroLast');
  const elChg = document.getElementById('heroChg');
  const elVol = document.getElementById('volNote');
  function syncHeroHeader() {
    const c = heroState.forming || heroState.candles[heroState.candles.length - 1];
    const base = heroState.candles[0].open;
    const diff = c.close - base;
    const pct = (diff / base) * 100;
    const up = diff >= 0;
    elLast.textContent = fmt(c.close);
    elLast.className = 'charthead__last num ' + (up ? 'up' : 'down');
    elChg.textContent = `${up ? '+' : ''}${fmt(diff)} (${up ? '+' : ''}${fmt(pct)}%)`;
    elChg.className = 'charthead__chg num ' + (up ? 'up' : 'down');
    elVol.textContent = 'vol ' + fmtVol(c.volume * 1e5) + ' · 90 candles · simulated';
  }

  // form a new candle: push forming -> drop oldest, start a fresh forming candle
  function advanceHero() {
    const cfg = TF[heroState.tf];
    const prev = heroState.candles[heroState.candles.length - 1].close;
    heroState.candles.push(stepCandle(prev, cfg.vol, cfg.drift));
    heroState.candles.shift();
    heroState.formT = 1; // will ease back to 0
  }

  /* timeframe + MA controls */
  document.querySelectorAll('.chip[data-tf]').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.chip[data-tf]').forEach((b) => b.classList.remove('is-active'));
      btn.classList.add('is-active');
      heroState.tf = btn.dataset.tf;
      const cfg = TF[heroState.tf];
      heroState.candles = seedSeries(90, heroState.candles[heroState.candles.length - 1].close, cfg.vol, cfg.drift);
      heroState.forming = null;
      syncHeroHeader();
    });
  });
  const maBtn = document.getElementById('toggleMA');
  maBtn.addEventListener('click', () => {
    heroState.showMA = !heroState.showMA;
    maBtn.setAttribute('aria-pressed', String(heroState.showMA));
  });

  /* ---------------------------------------------------------------------- *
   * 2. TICKER TAPE                                                          *
   * ---------------------------------------------------------------------- */
  const UNIVERSE = [
    { sym: 'HLX', name: 'Helix Dynamics', px: 182.4, vol: 0.004, drift: 0.0004 },
    { sym: 'NOVA', name: 'Nova Compute', px: 64.18, vol: 0.006, drift: 0.0006 },
    { sym: 'QBIT', name: 'Quantbit Labs', px: 311.7, vol: 0.009, drift: 0.0002 },
    { sym: 'VERT', name: 'Vertex Foods', px: 28.55, vol: 0.003, drift: 0.0001 },
    { sym: 'AURA', name: 'Aura Energy', px: 97.02, vol: 0.007, drift: 0.0005 },
    { sym: 'ORBT', name: 'Orbital Freight', px: 45.9, vol: 0.005, drift: -0.0002 },
    { sym: 'MICO', name: 'Micon Semi', px: 138.6, vol: 0.008, drift: 0.0007 },
    { sym: 'PEAK', name: 'Peak Capital', px: 76.4, vol: 0.004, drift: 0.0001 },
    { sym: 'RIVA', name: 'Riva Mobility', px: 52.3, vol: 0.01, drift: 0.0003 },
    { sym: 'GLOW', name: 'Glow Biotics', px: 19.85, vol: 0.012, drift: -0.0004 },
    { sym: 'DELT', name: 'Delta Cloud', px: 204.1, vol: 0.006, drift: 0.0006 },
    { sym: 'KRNO', name: 'Kronos AI', px: 421.9, vol: 0.011, drift: 0.0009 },
  ];
  // each gets a live price + session open
  const tape = UNIVERSE.map((u) => ({ ...u, open: u.px, last: u.px }));

  const tapeTrack = document.getElementById('tapeTrack');
  function buildTape() {
    const seg = tape.map(tickHTML).join('');
    tapeTrack.innerHTML = seg + seg; // duplicate for seamless marquee
  }
  function tickHTML(t) {
    const diff = t.last - t.open;
    const pct = (diff / t.open) * 100;
    const up = diff >= 0;
    return `<span class="tick"><span class="tick__sym">${t.sym}</span>` +
      `<span class="tick__px num">${fmt(t.last)}</span>` +
      `<span class="tick__chg ${up ? 'up' : 'down'} num"><span class="tick__arrow">${up ? '▲' : '▼'}</span>${up ? '+' : ''}${fmt(pct)}%</span></span>`;
  }
  function updateTape() {
    for (const t of tape) {
      const c = stepCandle(t.last, t.vol, t.drift);
      t.last = c.close;
    }
    // rewrite text content of existing nodes to avoid resetting marquee animation
    const nodes = tapeTrack.querySelectorAll('.tick');
    const half = nodes.length / 2;
    nodes.forEach((node, i) => {
      const t = tape[i % half];
      const diff = t.last - t.open, pct = (diff / t.open) * 100, up = diff >= 0;
      node.querySelector('.tick__px').textContent = fmt(t.last);
      const chg = node.querySelector('.tick__chg');
      chg.className = 'tick__chg ' + (up ? 'up' : 'down') + ' num';
      chg.innerHTML = `<span class="tick__arrow">${up ? '▲' : '▼'}</span>${up ? '+' : ''}${fmt(pct)}%`;
    });
  }

  /* ---------------------------------------------------------------------- *
   * 3. WATCHLIST SPARKLINE CARDS                                            *
   * ---------------------------------------------------------------------- */
  const WATCH = ['NOVA', 'QBIT', 'AURA', 'MICO', 'KRNO', 'RIVA'].map((s) => UNIVERSE.find((u) => u.sym === s));
  const cardsEl = document.getElementById('cards');
  const cardState = WATCH.map((u) => ({
    ...u, open: u.px, last: u.px,
    hist: seedSeries(48, u.px, u.vol, u.drift).map((c) => c.close),
  }));

  function buildCards() {
    cardsEl.innerHTML = cardState.map((c, i) => `
      <article class="card" data-i="${i}" tabindex="0" aria-label="${c.name} simulated quote">
        <div class="card__top">
          <span class="card__sym">${c.sym}</span>
          <span class="card__chg num" data-chg></span>
        </div>
        <div class="card__px num" data-px></div>
        <canvas class="card__spark" data-spark></canvas>
        <div class="card__name">${c.name}</div>
      </article>`).join('');
    // size canvases
    cardsEl.querySelectorAll('.card').forEach((card) => {
      const cv = card.querySelector('[data-spark]');
      sizeSpark(cv);
      drawCard(parseInt(card.dataset.i, 10), false);
    });
  }
  function sizeSpark(cv) {
    const r = cv.getBoundingClientRect();
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    cv.width = Math.max(1, Math.round(r.width * dpr));
    cv.height = Math.max(1, Math.round(r.height * dpr));
    const c = cv.getContext('2d');
    c.setTransform(dpr, 0, 0, dpr, 0, 0);
    cv._w = r.width; cv._h = r.height;
  }

  function drawCard(i, flash) {
    const card = cardsEl.querySelector(`.card[data-i="${i}"]`);
    if (!card) return;
    const s = cardState[i];
    const diff = s.last - s.open, pct = (diff / s.open) * 100, up = diff >= 0;
    const pxEl = card.querySelector('[data-px]');
    const chgEl = card.querySelector('[data-chg]');
    pxEl.textContent = fmt(s.last);
    chgEl.textContent = `${up ? '+' : ''}${fmt(pct)}%`;
    chgEl.className = 'card__chg num ' + (up ? 'up' : 'down');
    if (flash) {
      pxEl.classList.remove('flash-up', 'flash-down');
      void pxEl.offsetWidth;
      pxEl.classList.add(up ? 'flash-up' : 'flash-down');
    }
    // spark
    const cv = card.querySelector('[data-spark]');
    const c = cv.getContext('2d');
    const W = cv._w, H = cv._h;
    c.clearRect(0, 0, W, H);
    const data = s.hist;
    let mn = Math.min(...data), mx = Math.max(...data);
    const pad = (mx - mn) * 0.15 || 1; mn -= pad; mx += pad;
    const X = (k) => (k / (data.length - 1)) * (W - 2) + 1;
    const Y = (v) => H - 3 - ((v - mn) / (mx - mn)) * (H - 6);
    const col = up ? COLORS.up : COLORS.down;
    // area fill
    const grad = c.createLinearGradient(0, 0, 0, H);
    grad.addColorStop(0, hexA(col, 0.28));
    grad.addColorStop(1, hexA(col, 0));
    c.beginPath();
    c.moveTo(X(0), Y(data[0]));
    for (let k = 1; k < data.length; k++) c.lineTo(X(k), Y(data[k]));
    c.lineTo(X(data.length - 1), H); c.lineTo(X(0), H); c.closePath();
    c.fillStyle = grad; c.fill();
    // line
    c.beginPath();
    c.moveTo(X(0), Y(data[0]));
    for (let k = 1; k < data.length; k++) c.lineTo(X(k), Y(data[k]));
    c.strokeStyle = col; c.lineWidth = 1.5;
    c.shadowColor = up ? getCSS('--up-glow') : getCSS('--down-glow'); c.shadowBlur = 6;
    c.stroke(); c.shadowBlur = 0;
    // head dot
    c.beginPath(); c.arc(X(data.length - 1), Y(data[data.length - 1]), 2.2, 0, Math.PI * 2);
    c.fillStyle = col; c.fill();
  }

  function updateCards() {
    cardState.forEach((s, i) => {
      const step = stepCandle(s.last, s.vol, s.drift);
      s.last = step.close;
      s.hist.push(s.last);
      if (s.hist.length > 48) s.hist.shift();
      drawCard(i, true);
    });
  }

  /* ---------------------------------------------------------------------- *
   * 4. SECTOR HEATMAP                                                       *
   * ---------------------------------------------------------------------- */
  const SECTORS = [
    { sym: 'TECH', name: 'Technology' }, { sym: 'SEMI', name: 'Semiconductors' },
    { sym: 'ENER', name: 'Energy' }, { sym: 'FINS', name: 'Financials' },
    { sym: 'HLTH', name: 'Health Care' }, { sym: 'CONS', name: 'Consumer' },
    { sym: 'INDU', name: 'Industrials' }, { sym: 'MATL', name: 'Materials' },
    { sym: 'UTIL', name: 'Utilities' }, { sym: 'REAL', name: 'Real Estate' },
    { sym: 'COMM', name: 'Comms' }, { sym: 'STPL', name: 'Staples' },
  ].map((s) => ({ ...s, chg: rnd(-2.4, 2.4), target: rnd(-2.4, 2.4) }));

  const heatEl = document.getElementById('heatmap');
  function buildHeat() {
    heatEl.innerHTML = SECTORS.map((s, i) =>
      `<div class="tile" role="listitem" data-i="${i}" aria-label="${s.name} sector, simulated change">
        <div><div class="tile__sym">${s.sym}</div><div class="tile__name">${s.name}</div></div>
        <div class="tile__chg num" data-hchg></div>
      </div>`).join('');
    SECTORS.forEach((_, i) => paintTile(i));
  }
  // map a % change to a green/red color
  function heatColor(chg) {
    const t = Math.max(-1, Math.min(1, chg / 3)); // -1..1
    const up = getCSS('--up'), down = getCSS('--down');
    const [r1, g1, b1] = hexRGB(up), [r2, g2, b2] = hexRGB(down);
    const intensity = Math.min(1, Math.abs(t));
    let r, g, b;
    if (t >= 0) { r = r1; g = g1; b = b1; } else { r = r2; g = g2; b = b2; }
    // blend toward dark base by intensity
    const baseR = 12, baseG = 18, baseB = 33;
    const a = 0.14 + intensity * 0.72;
    return `rgb(${Math.round(baseR + (r - baseR) * a)},${Math.round(baseG + (g - baseG) * a)},${Math.round(baseB + (b - baseB) * a)})`;
  }
  function paintTile(i) {
    const s = SECTORS[i];
    const tile = heatEl.querySelector(`.tile[data-i="${i}"]`);
    if (!tile) return;
    tile.style.background = heatColor(s.chg);
    const up = s.chg >= 0;
    tile.style.color = Math.abs(s.chg) > 1.4 ? '#04110b' : (up ? '#cfffe9' : '#ffd9dd');
    const chgEl = tile.querySelector('[data-hchg]');
    chgEl.textContent = `${up ? '+' : ''}${s.chg.toFixed(2)}%`;
  }
  function updateHeat() {
    SECTORS.forEach((s, i) => {
      if (Math.abs(s.chg - s.target) < 0.05) s.target = rnd(-2.6, 2.6);
      s.chg += (s.target - s.chg) * 0.08 + gauss() * 0.04;
      s.chg = Math.max(-3, Math.min(3, s.chg));
      paintTile(i);
    });
  }

  /* ---------------------------------------------------------------------- *
   * INDEX STATS (top right)                                                 *
   * ---------------------------------------------------------------------- */
  const idxEl = document.getElementById('indexStats');
  const indices = [
    { k: 'HELIX 500', v: 4182.6, base: 4182.6, vol: 0.0012 },
    { k: 'NOVA-100', v: 13970.2, base: 13970.2, vol: 0.0016 },
    { k: 'VLX VOL', v: 16.4, base: 16.4, vol: 0.01, raw: true },
  ];
  function buildIndex() {
    idxEl.innerHTML = indices.map((x, i) =>
      `<div class="stat"><dt class="stat__k">${x.k}</dt><dd class="stat__v num" data-idx="${i}"></dd></div>`).join('');
    indices.forEach((_, i) => paintIndex(i));
  }
  function paintIndex(i) {
    const x = indices[i];
    const el = idxEl.querySelector(`[data-idx="${i}"]`);
    const diff = x.v - x.base, pct = (diff / x.base) * 100, up = diff >= 0;
    if (x.raw) {
      el.textContent = fmt(x.v, 2);
      el.className = 'stat__v num ' + (up ? 'down' : 'up'); // vol up = "risk" red-ish
    } else {
      el.textContent = fmt(x.v, 1) + '  ' + (up ? '+' : '') + fmt(pct, 2) + '%';
      el.className = 'stat__v num ' + (up ? 'up' : 'down');
    }
  }
  function updateIndex() {
    indices.forEach((x, i) => { x.v = stepCandle(x.v, x.vol, 0.0002).close; paintIndex(i); });
  }

  /* ---------------------------------------------------------------------- *
   * CLOCK                                                                   *
   * ---------------------------------------------------------------------- */
  const clockEl = document.getElementById('clock');
  function tickClock() {
    const d = new Date();
    const p = (n) => String(n).padStart(2, '0');
    clockEl.textContent = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())} SIM`;
  }

  /* ---------------------------------------------------------------------- *
   * COLOR UTILS                                                             *
   * ---------------------------------------------------------------------- */
  function hexRGB(hex) {
    hex = hex.replace('#', '');
    if (hex.length === 3) hex = hex.split('').map((c) => c + c).join('');
    return [parseInt(hex.slice(0, 2), 16), parseInt(hex.slice(2, 4), 16), parseInt(hex.slice(4, 6), 16)];
  }
  function hexA(hex, a) { const [r, g, b] = hexRGB(hex); return `rgba(${r},${g},${b},${a})`; }
  function roundRect(x, y, w, h, r) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  /* ---------------------------------------------------------------------- *
   * MAIN LOOP                                                               *
   * ---------------------------------------------------------------------- */
  let lastForm = 0;
  const FORM_INTERVAL = 2200; // new hero candle cadence (ms)

  function frame(now) {
    // ease the smooth-scroll offset back toward 0
    if (heroState.formT > 0) {
      heroState.formT *= REDUCED ? 0 : 0.86;
      if (heroState.formT < 0.001) heroState.formT = 0;
    }
    // new candle?
    if (now - lastForm > FORM_INTERVAL) {
      advanceHero();
      lastForm = now;
    }
    // animate the forming candle drifting from the last close
    const cfg = TF[heroState.tf];
    const base = heroState.candles[heroState.candles.length - 1].close;
    if (!heroState.forming || heroState.formT > 0.5) {
      heroState.forming = { open: base, high: base, low: base, close: base, volume: 1 };
    }
    // wiggle the forming candle each frame
    const f = heroState.forming;
    const nx = f.close * (1 + cfg.drift * 0.1 + cfg.vol * 0.18 * gauss());
    f.close = nx;
    f.high = Math.max(f.high, nx); f.low = Math.min(f.low, nx);
    f.volume = Math.min(2, f.volume + 0.01);

    drawHero();
    syncHeroHeader();

    if (!REDUCED) requestAnimationFrame(frame);
  }

  /* ---------------------------------------------------------------------- *
   * BOOT                                                                    *
   * ---------------------------------------------------------------------- */
  function boot() {
    resize();
    buildTape();
    buildCards();
    buildHeat();
    buildIndex();
    syncHeroHeader();
    tickClock();

    // periodic updates (feel alive)
    setInterval(updateTape, 1400);
    setInterval(updateCards, 1700);
    setInterval(updateHeat, 900);
    setInterval(updateIndex, 1200);
    setInterval(tickClock, 1000);

    // resize spark canvases on window resize (debounced)
    let rt;
    window.addEventListener('resize', () => {
      clearTimeout(rt);
      rt = setTimeout(() => {
        cardsEl.querySelectorAll('[data-spark]').forEach(sizeSpark);
        cardState.forEach((_, i) => drawCard(i, false));
      }, 150);
    });

    if (REDUCED) {
      // single static render, plus slow non-animated ticks already via intervals
      heroState.forming = null;
      drawHero();
    } else {
      requestAnimationFrame(frame);
    }
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
