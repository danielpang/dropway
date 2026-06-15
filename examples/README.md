<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Shipped Examples

A gallery of **self-contained static sites** — the kind of thing Shipped exists to
put on a live URL in one command. Each folder is pure HTML/CSS/client-side JS with
**no build step, no server, and no runtime API calls**: every dataset is embedded
inline and every visual is generated in code. The only external requests are pinned
CDN libraries (Three.js) and Google Fonts; both degrade gracefully.

They double as deploy fixtures: each folder is a ready-to-ship `dist/`.

## The gallery

| Example | What it is | Tech |
|---|---|---|
| [`solar-system/`](./solar-system/) | Interactive 3D orrery — the Sun to Pluto, orbits, Saturn's rings, a starfield, click-for-facts HUD, play/pause + speed. | Three.js (CDN), procedural materials, OrbitControls |
| [`stock-market/`](./stock-market/) | A live-feeling trading terminal — animated candlesticks with a crosshair, a ticker tape, sparkline cards, a sector heatmap. **Simulated data.** | Hand-drawn `<canvas>`, vanilla JS random-walk model |
| [`world-series/`](./world-series/) | A 2025 World Series spray chart — an overhead SVG field with every hit plotted, filter by team/game/hit-type, live stats. **Illustrative sample data, not official.** | Pure SVG + vanilla JS |
| [`particle-flow/`](./particle-flow/) | Full-screen generative art — thousands of particles flowing through a simplex-noise field with fading trails; mouse swirls, click cycles palettes. | `<canvas>`, inline simplex noise |
| [`periodic-table/`](./periodic-table/) | All 118 elements in the correct grid, color-coded by category, search + filter + color-by-state, and a detail modal per element. | CSS grid, vanilla JS, inline dataset |
| [`synthwave-sunset/`](./synthwave-sunset/) | A retro outrun sunset — scanline sun, scrolling neon grid, twinkling stars, chrome title, CRT overlay. **Zero JavaScript.** | Pure CSS art |

> **On the data:** `stock-market` and `world-series` use generated, clearly-labeled
> sample data — they're visualization showpieces, not feeds of real quotes or
> official game stats.

## Viewing one locally

Each example works by opening `index.html` directly, but a tiny static server
avoids browser file-URL quirks (and is closer to how Shipped serves them):

```bash
cd examples/solar-system
python3 -m http.server 8000
# open http://localhost:8000
```

## Shipping one

Each folder is already a deployable static bundle:

```bash
shipped deploy ./examples/solar-system
```
