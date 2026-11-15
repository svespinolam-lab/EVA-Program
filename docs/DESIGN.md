# EVA · Shared design system (phone app + EVA Station)

Both programs load the same files from `web/shared/`, so they read as **one system**:

| File | Contains |
|---|---|
| `css/tokens.css` | Colours, type, spacing, radii, motion — light theme by default + dark "cabin mode" |
| `css/base.css` | Reset, risk levels (`data-level="0|1|2|3|off"` → `--lvl`, `--lvl-soft`), utilities |
| `css/controls.css` | Cards, buttons, badges, segmented controls, tiles, ring gauge, findings, timeline, lists, forms |
| `js/icons.js` | One line-icon set (24 px grid, 1.75 stroke) → `App.icon('heart')` |
| `logo.png` | EVA logo (caduceus with orbit) |

## Identity

Inspired by the institutional aerospace language — **no official NASA logos, names or insignia**.

| Token | Value | Use |
|---|---|---|
| `--brand-blue` / `--accent` | `#0B3D91` | Predominant colour: active navigation, primary actions, key data |
| `--brand-navy` | `#061A36` | RESULT bands, splash, toasts, main text |
| `--brand-red` | `#FC3D21` | Logo only. Alerts use `--critical` (`#C8250E` text, 5.6:1) |
| `--bg` | `#F2F4F7` | Space grey background |
| `--surface` | `#FFFFFF` | Cards and bars |
| `--brand-grey` | `#687386` | Inactive icons (text greys are darker for 4.5:1) |

Dark cabin mode (`data-theme="dark"`): deep navy surfaces for a dim cabin; same token names.

**Type:** Helvetica Neue → Inter → Arial for text; **DM Mono** for every figure, unit, time and code.

## Coherence rules (PC ↔ phone)

1. One token file: colours, radii (cards 16 · controls 12–14 · pills), shadows and 8-pt spacing come only from `tokens.css`.
2. Same module names and icons: Log / Diagnosis / Profile / Settings on the phone; Triage (= Diagnosis of the whole crew), Pre-mission, Protocols, Crew (= Profile), Reports on the PC.
3. One risk vocabulary: Nominal · Caution · High risk · Critical — always colour + icon shape + word. Station statuses map to it: STABLE · VULNERABLE · RISK · EMERGENCY.
4. Figures in DM Mono with one format: `14 NOV 2026 · 08:42`, `72 bpm`, `118/76 mmHg`.
5. Equivalent components: IPV ring, finding rows, badges and sensor tiles are identical; the desktop rearranges them in a grid and the phone's bottom tab bar becomes the side bar.
6. Orbital motifs are secondary and never sit on top of medical data. Red never decorates.
