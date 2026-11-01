---
name: Pyro
description: API client and load tester that reads latency as heat
colors:
  iron-0: "oklch(0.155 0.008 250)"
  iron-1: "oklch(0.19 0.009 250)"
  iron-2: "oklch(0.23 0.011 250)"
  iron-3: "oklch(0.28 0.012 250)"
  rule: "oklch(0.3 0.012 250)"
  rule-strong: "oklch(0.38 0.014 250)"
  brick-0: "oklch(0.2 0.028 35)"
  brick-1: "oklch(0.24 0.034 35)"
  brick-2: "oklch(0.29 0.042 35)"
  brick-rule: "oklch(0.33 0.045 35)"
  brick-text: "oklch(0.9 0.02 60)"
  brick-muted: "oklch(0.72 0.035 50)"
  text: "oklch(0.93 0.006 250)"
  text-muted: "oklch(0.71 0.012 250)"
  text-faint: "oklch(0.58 0.012 250)"
  flame: "oklch(0.67 0.2 42)"
  flame-hover: "oklch(0.73 0.19 46)"
  flame-ink: "oklch(0.2 0.04 40)"
  heat-cold: "oklch(0.62 0.03 240)"
  heat-ember: "oklch(0.5 0.13 32)"
  heat-cherry: "oklch(0.6 0.19 30)"
  heat-orange: "oklch(0.77 0.16 66)"
  heat-yellow: "oklch(0.89 0.14 96)"
  heat-white: "oklch(0.97 0.035 95)"
  ok: "oklch(0.76 0.13 155)"
  warn: "oklch(0.83 0.13 85)"
  error: "oklch(0.66 0.2 18)"
  method-get: "oklch(0.78 0.12 155)"
  method-post: "oklch(0.82 0.13 85)"
  method-put: "oklch(0.75 0.1 240)"
  method-patch: "oklch(0.76 0.11 300)"
  method-delete: "oklch(0.7 0.17 22)"
  stream: "oklch(0.78 0.1 200)"
typography:
  title:
    fontFamily: "Archivo Variable, Archivo, system-ui, sans-serif"
    fontSize: "17px"
    fontWeight: 600
  heading:
    fontFamily: "Archivo Variable, Archivo, system-ui, sans-serif"
    fontSize: "20px"
    fontWeight: 600
  body:
    fontFamily: "Archivo Variable, Archivo, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 400
    lineHeight: 1.45
    fontFeature: "tnum"
  label:
    fontFamily: "Archivo Variable, Archivo, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 500
  code:
    fontFamily: "JetBrains Mono, ui-monospace, monospace"
    fontSize: "12.5px"
    fontWeight: 400
    lineHeight: 1.55
  badge:
    fontFamily: "JetBrains Mono, ui-monospace, monospace"
    fontSize: "10.5px"
    fontWeight: 600
rounded:
  sm: "3px"
  md: "4px"
spacing:
  row: "28px"
  gutter: "18px"
  rail: "272px"
components:
  button-primary:
    backgroundColor: "{colors.flame}"
    textColor: "{colors.flame-ink}"
    rounded: "{rounded.md}"
    height: "30px"
    padding: "0 12px"
  button-primary-hover:
    backgroundColor: "{colors.flame-hover}"
  button-ghost:
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    height: "30px"
    padding: "0 12px"
  input:
    backgroundColor: "{colors.iron-1}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    height: "26px"
  rail-row:
    backgroundColor: "{colors.brick-0}"
    textColor: "{colors.brick-text}"
    height: "{spacing.row}"
  rail-row-active:
    backgroundColor: "{colors.brick-2}"
  heat-chip:
    typography: "{typography.code}"
    rounded: "{rounded.sm}"
    padding: "2px 8px"
---

# Design System: Pyro

## Overview

**Creative North Star: "The Pyrometer's Eye"**

A kiln operator judges metal by its glow: dull red, cherry, orange, yellow, white. Pyro reads latency the same way. Every measured duration (a response time, a phase of a request, later a load-test percentile) sits on one incandescence scale, so how slow something ran is visible before the number is read.

Everything else stays out of the way. The content area is cold iron, a cool near-black. The shell (top bar, rail, status bar) is warm kiln brick, so the place where work happens and the place where you navigate never blur. The screen is dense and quiet: 28px rows, 1px rules, small radii. Standard controls throughout. It is a working tool that sits beside an IDE all day, and dark is chosen for that scene, not for style.

**Key Characteristics:**
- One heat ramp, used only on measured data, always with a visible legend.
- Cold iron content ground, warm brick shell.
- A single flame-orange accent for chrome: primary action, selection, focus.
- Dense rows, tabular numerals, monospace only for code, URLs, paths and measurements.
- State colours (ok, warn, error) appear only where they carry meaning.

## Colors

A cool, near-neutral iron field and a warm brick frame, with heat reserved for numbers.

### Primary
- **Flame** (`flame`): the only chrome accent: the Send button, selected tab underline, focus rings, checkbox fill, selection highlight, links, the "unsaved" marker. Deliberately deeper and redder than any heat step so a control never reads as a measurement.

### Heat scale (data only)
- **Cold steel** (`heat-cold`): under 100 ms.
- **Ember** (`heat-ember`): 100–300 ms.
- **Cherry** (`heat-cherry`): 300–600 ms.
- **Orange heat** (`heat-orange`): 600 ms–1 s.
- **Yellow heat** (`heat-yellow`): 1–2 s.
- **White heat** (`heat-white`): over 2 s.

### Neutral
- **Cold iron** (`iron-0` … `iron-3`): content ground, then raised layers for inputs, code blocks, splitters and hover.
- **Rules** (`rule`, `rule-strong`): 1px dividers and input strokes; strong for table heads and segmented controls.
- **Kiln brick** (`brick-0` … `brick-2`, `brick-rule`): top bar, rail and status bar; brick-2 marks the selected row.
- **Brick text / muted** (`brick-text`, `brick-muted`): text on the shell.
- **Text / muted / faint** (`text`, `text-muted`, `text-faint`): text on iron; faint for placeholders and paths.

### State and protocol
- **OK / Warn / Error** (`ok`, `warn`, `error`): 2xx/3xx/4xx/5xx status text, notices, history results. Errors always pair crimson with an icon or the word "failed".
- **Method colours** (`method-*`) and **Stream** (`stream`): the HTTP method and WS/SIO badges.

### Light theme
Dark is the default and the reference. A light theme ("daylight in the foundry") keeps every
role: pale cool steel for content (`iron-*` near white), pale warm brick for the shell, dark
text, a deeper flame accent with light ink, and darker state and method colours so they hold
contrast on white. Heat keeps its order (hotter still reads hotter); cold steel deepens so
white chip text stays legible, and white heat gets a thin warm outline so it shows on a pale
ground. Chart colours are re-validated with the dataviz palette script against `#fafafb`
(series `#008f86`, baseline `#7a58c9`, percentiles `#8db6e3` → `#4a86cc` → `#1f5592`, which
darken toward p99 because darker is more salient on white). The status bar's theme menu
offers System (follows the OS, the default), Dark and Light; the choice is per browser. The
HTML load report stays dark.

### Named Rules
**The Quarantine Rule.** Heat colours appear only on measured data: the heat chip, the heated bar, legend swatches, timing values. Never on buttons, menus, borders or decoration.

**The Legend Rule.** Every place that shows heat can reach its legend: the Timing tab and the welcome screen carry the full scale.

**The Lit-Until-Seen Rule.** A response at orange heat or hotter keeps its glow until the user opens Timing. A breach is not allowed to fade unnoticed.

## Typography

**UI Font:** Archivo Variable (with system-ui)
**Mono Font:** JetBrains Mono (with ui-monospace)

**Character:** Archivo is an industrial grotesque with real tabular figures, sturdy at small sizes in dense rows. JetBrains Mono is used only for what is literally code or data: URLs, request bodies, header values, paths, method badges and measurements.

### Hierarchy
- **Heading** (600, 20px): page-level titles in main (welcome, import result, blockers).
- **Title** (600, 17px): the request name, editable in place.
- **Body** (400, 13.5px, 1.45): everything else; tabular numerals on by default.
- **Label** (500, 12px): field labels, tab counts, meta text.
- **Code** (400, 12.5px, 1.55): URL bar, body editors, response bodies, tables of headers.
- **Badge** (600, 10.5px mono): method and protocol badges.

### Named Rules
**The Honest Mono Rule.** Monospace marks code, URLs, paths and numbers. It is never used for decoration or to make text look "technical".

## Layout

A fixed app shell: a 44px brick top bar, a 272px brick rail on the left, a 24px status bar, and the iron main area. Main splits into the request editor above and the response below, with a draggable, keyboard-adjustable splitter (20–80%, remembered per browser). Content gutters are 18px; rows are 28px. Below 760px the rail stacks above main at up to a third of the height, the workspace name and environment label hide, and the path hint drops from the status bar. Grid tracks use `minmax(0, 1fr)` so long URLs scroll inside their field and never push the page sideways.

## Elevation & Depth

Flat. Depth comes from tonal layering (iron-0 to iron-3, brick-0 to brick-2) and 1px rules, not shadows. The only glow in the system is heat: a lit heat chip and hot segments of the heated bar cast a soft light in their own colour.

### Shadow Vocabulary
- **Heat glow** (on lit chips at orange, yellow and white heat; on hot heated-bar segments): a soft, colour-matched halo with a slight downward offset, so it reads as light thrown by the measurement.

### Named Rules
**The Only-Heat-Glows Rule.** Nothing else in the interface casts light or shadow.

## Shapes

Small, firm corners: 3px on chips and badges, 4px on buttons, inputs and panels. Inputs are 1px stroked fields. The URL bar joins method, URL and Send into one segmented control with only the outer corners rounded. No cards, no pills.

## Components

### Buttons
- **Shape:** gently squared (4px), 30px tall.
- **Primary:** flame fill with dark flame-ink text, semibold. Used once per area: Send, Done.
- **Hover / Focus:** lighter flame on hover; a 2px flame focus ring.
- **Ghost:** 1px strong rule, iron hover fill (Save, Format). **Shell:** the same on brick (Import).
- **Disabled:** iron-3 fill with faint text.

### Inputs / Fields
- **Style:** iron-1 fill, 1px rule stroke, 4px radius; mono for any value that is code or data.
- **Focus:** flame border plus a 1px flame ring.
- **Disabled rows:** in key-value tables, a cleared checkbox greys the row and strikes the text through.

### Navigation
- **Rail:** brick shell with Collections and History tabs (flame underline when selected). Tree rows are 28px; the selected row takes brick-2 with brighter, medium-weight text. A + appears on hover or focus of a collection or folder to add a request.
- **Tabs:** text tabs with a 2px flame underline; counts sit in small iron-3 chips.

### Heat chip (signature)
The response time, set in mono on its heat colour. Cold steel through cherry read as solid chips; orange and hotter are lit with a glow until the Timing tab is opened. Clicking it opens Timing.

### Heated bar (signature)
The timing breakdown drawn to scale: DNS, connect, TLS, send, wait and download as segments on one bar, each coloured by its own duration, so the phase that made a request hot glows. Always followed by the phase table and the legend.

## Do's and Don'ts

### Do:
- **Do** put every new measurement (load percentiles, run durations) on the same heat scale with the same thresholds and a reachable legend.
- **Do** keep the shell brick and the content iron; new panels inside main stay iron.
- **Do** use flame for exactly one primary action per area.
- **Do** pair every error colour with an icon or a word.
- **Do** keep tracks `minmax(0, 1fr)` and let long values scroll inside their field.

### Don't:
- **Don't** use heat colours on chrome, borders or decoration (The Quarantine Rule).
- **Don't** mark a selected row with a coloured side stripe; use the brick-2 fill.
- **Don't** add shadows for elevation; layer tones instead.
- **Don't** use monospace for prose or labels.
- **Don't** introduce a second accent colour.
