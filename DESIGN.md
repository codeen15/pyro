---
name: Pyro
description: API client and load tester on Ant Design, with latency read as heat
colors:
  page: "#f5f5f5"
  card: "#ffffff"
  fill: "#fafafa"
  rule: "#f0f0f0"
  rule-strong: "#d9d9d9"
  text: "rgba(0, 0, 0, 0.88)"
  text-muted: "rgba(0, 0, 0, 0.65)"
  text-faint: "rgba(0, 0, 0, 0.45)"
  primary: "#cf1920"
  primary-hover: "#db4245"
  primary-soft: "#fff1f0"
  heat-cold: "oklch(0.55 0.03 240)"
  heat-ember: "oklch(0.47 0.13 32)"
  heat-cherry: "oklch(0.56 0.19 28)"
  heat-orange: "oklch(0.7 0.16 62)"
  heat-yellow: "oklch(0.83 0.15 92)"
  heat-white: "oklch(0.965 0.045 95)"
  ok: "#389e0d"
  warn: "#d48806"
  error: "#cf1322"
  live: "#52c41a"
  method-get: "#389e0d"
  method-post: "#d48806"
  method-put: "#1677ff"
  method-patch: "#722ed1"
  method-delete: "#cf1322"
  stream-sio: "#08979c"
  stream-ws: "#531dab"
typography:
  heading:
    fontFamily: "-apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: "28px"
  body:
    fontFamily: "-apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5714
    fontFeature: "tnum"
  meta:
    fontFamily: "-apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "12px"
    fontWeight: 400
  code:
    fontFamily: "JetBrains Mono, ui-monospace, monospace"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.6
  badge:
    fontFamily: "JetBrains Mono, ui-monospace, monospace"
    fontSize: "10.5px"
    fontWeight: 600
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
spacing:
  row: "32px"
  gutter: "16px"
  card-padding: "20px 24px"
  header: "56px"
  rail: "300px"
  status: "28px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    height: "32px"
  url-bar:
    height: "40px"
    rounded: "{rounded.lg}"
  card:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.lg}"
    padding: "{spacing.card-padding}"
  rail-row-active:
    backgroundColor: "{colors.primary-soft}"
    textColor: "{colors.primary}"
---

# Design System: Pyro

## Overview

Pyro's interface is built from **Ant Design 6** components (`antd`, MIT) with Ant's stock look:
a light grey page, white cards with 1px hairlines and 8px corners, 14px system type and 32px
controls. The brand colour is the red of the Pyro logo (a flame with a lightning bolt), used as
Ant's `colorPrimary`. Dark mode uses Ant's dark algorithm with the same roles.

One signature survives from the earlier Pyrometry design: **measured time is read as heat**.
Every duration (response time, request phases, load percentiles, ack round trips) carries a
small swatch from one incandescence scale, from cold steel to white heat, beside neutral text.

**Key characteristics:**
- antd components first. A custom component exists only where antd has nothing suitable (the heat swatch,
  heated timing bar, message log rows, method badges).
- Red primary for chrome (primary buttons, selected tab, selected row, focus ring, links).
- Heat only on measured data, always as a swatch beside neutral text, with a reachable legend.
- Monospace only for code, URLs, paths, method badges and measurements.

## Implementation

- **Theme:** `web/src/theme.tsx` sets `ConfigProvider` tokens (`colorPrimary #cf1920`, radii
  6/8, system font, JetBrains Mono for code). It switches `defaultAlgorithm` / `darkAlgorithm`
  from the theme preference, and wraps the app in antd's `<App>` so `App.useApp()` gives
  `message` and `modal`. Never use `alert`, `confirm` or `prompt`.
- **Icons:** [Hugeicons](https://hugeicons.com) free set (`@hugeicons/react` +
  `@hugeicons/core-free-icons`, MIT), stroke 1.75. `web/src/icons.tsx` re-exports them under
  the names the components use, sized 1em by default (or a `size` prop) with class `hi`.
  Icons inside antd components (select arrows, alert and modal icons) stay antd's own.
- **Custom CSS:** `web/src/styles.css` holds layout and the few custom pieces, using tokens
  that mirror Ant's palette (`--iron-0` page, `--iron-1` card, `--iron-2` fill, `--rule`,
  `--rule-strong`, `--text*`, `--accent*`, `--ok/warn/error*`). Each token is redefined
  under `:root[data-theme="dark"]`. Write custom CSS with these tokens, not hex values.
- **Logo:** `web/src/assets/pyro-logo.png` (header, welcome) and `web/public/favicon.png`.

## Colors

- **Primary** (`#cf1920`, hover `#db4245`, soft `#fff1f0`): primary buttons (Send, Connect,
  Start, Done), the active tab underline, the selected sidebar row (soft fill, red text),
  focus rings, links, the "unsaved" marker and a dirty Save button's outline.
- **Neutrals:** page `#f5f5f5`, cards white, fills `#fafafa` (method select, table heads,
  code blocks), split lines `#f0f0f0`, control borders `#d9d9d9`. Text uses alpha
  black at .88, .65 and .45.
- **State:** Ant's success, warning and error colours for status tags, alerts and history
  results. Errors always pair the colour with an icon or a word.
- **Methods and protocols:** GET green, POST gold, PUT blue, PATCH purple, DELETE red as mono
  text tags. Socket.IO is cyan (event names are cyan tags) and WebSocket is purple.
- **Environments:** each environment gets a stable coloured dot in the header select. A name
  containing "prod" is always red.

### Heat scale (data only)
| Step | Threshold |
|---|---|
| Cold steel | under 100 ms |
| Ember | 100–300 ms |
| Cherry | 300–600 ms |
| Orange heat | 600 ms–1 s |
| Yellow heat | 1–2 s |
| White heat | over 2 s |

Thresholds live in `web/src/heat.ts`.

### Charts
Chart colours are `--viz-*`, validated with the dataviz palette script against the card
surface (white in light mode, `#141414` in dark). In light mode they are series `#008f86`,
baseline `#7a58c9`, and percentiles `#8db6e3` → `#4a86cc` → `#1f5592`. The dark values are in
`styles.css`. Charts never use dual axes, and text is never set in a data colour.

### Named rules
**The Quarantine Rule.** Heat colours appear only on measured data. They never appear on
buttons, menus, borders or decoration.

**The Swatch Rule.** A measurement is neutral text with a heat swatch beside it. The number
itself is never set in a heat colour.

**The Legend Rule.** Every place that shows heat can reach its legend. The Timing tab and the
welcome screen carry the full scale.

**The Lit-Until-Seen Rule.** A response at orange heat or hotter keeps a halo on its swatch
until the user opens Timing.

## Typography

The system UI font stack at 14px with tabular numerals, and JetBrains Mono at 13px for URLs,
bodies, header values, paths and measurements.
- **Heading** (600, 20px/28px): request names (editable in place, with a pencil hint) and page
  titles.
- **Body** (400, 14px).
- **Meta** (12px, faint): slugs, counts, sizes, timestamps.
- **Badge** (600, 10.5px mono): method and protocol tags in the tree and history.

## Layout

A fixed shell:
- a 56px white header: logo, workspace, environment select, icon buttons and Import;
- a 300px white sidebar with Collections, History, Load and Plugins tabs;
- a 28px status bar: engine status, host, workspace path and a theme menu (System / Light / Dark);
- the grey main area.

Main pads 16px. The request card and response card are separated by a 16px draggable
gutter (20–80%, remembered per browser). Full-page views (settings, variables, load tests,
plugins, import) render `.page` > `.card` sections.

Below 760px the sidebar stacks above main, labels collapse to icons, the path and host hide,
and grids fall to one column. Grid tracks use `minmax(0, 1fr)`, so long values scroll inside
their field rather than pushing the page sideways.

## Components

- **Request card:**
  - an editable title, with the mono slug beneath;
  - a fixed request-type label (HTTP / WebSocket / Socket.IO / plugin type) and a Save button
    (Saved when clean). The type is chosen once, from cards in the New request dialog, and
    can't be changed afterwards;
  - a `Space.Compact` URL bar, 40px tall: method `Select` on a fill, mono `Input`, primary
    Send at least 124px wide;
  - antd `Tabs` with faint counts.
- **Key-value tables:** a bordered table with a fill header, a `Checkbox` column, borderless
  mono `Input`s and a delete icon. A trailing blank row adds entries.
- **Response card:**
  - a 52px header: "Response", status `Tag`, time with heat swatch, size, protocol, a test
    summary, Save as example, and the part `Tabs` on the right;
  - the body in a fill code block, with a Pretty/Raw `Segmented` toggle;
  - an empty state of a 56px circle icon plus text.
- **Realtime log card:**
  - a status dot with a halo (green when live, gold while connecting), the label and meta;
  - a filter input, a Follow checkbox and clear;
  - rows laid out as icon / time / event tag / payload / size;
  - an empty state with an outlined red Connect button.
- **Heated bar:** the timing breakdown to scale, each phase coloured by its own duration,
  followed by the phase table and legend.

## Do's and Don'ts

- **Buttons:** one size everywhere, antd's default 32px (Send, Start, tree row actions,
  inline links and dialog buttons alike). Don't pass `size` to a Button.
- **Variables in fields:** URL, key/value, body, auth and message fields highlight
  `{{name}}` in place: green when it resolves (runtime → environment → folders → collection →
  globals, as the engine does), red when it doesn't, purple for dynamic `{{$…}}` values.
  Hovering one opens a card with its value and scope; it can be edited there (saved to its
  scope's file) or, when missing, added to the environment, folder, collection or globals.

### Do:
- **Do** reach for an antd component before writing a custom one, and theme it through tokens
  rather than CSS overrides.
- **Do** put new measurements on the heat scale as swatches, with a reachable legend.
- **Do** use one primary button per area.
- **Do** keep accessible names (aria-labels) on icon-only buttons. The e2e tests rely on them.

### Don't:
- **Don't** use heat colours on chrome, or set numbers in heat colours.
- **Don't** introduce a second brand colour; status colours are for status only.
- **Don't** use monospace for prose or labels.
- **Don't** hard-code hex values in custom CSS; use the tokens so dark mode follows.
