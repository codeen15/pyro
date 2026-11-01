---
version: 1
slug: "web-src-app-tsx"
primary_target: "web/src/App.tsx"
related_targets: ["web/src/styles.css"]
---

# Workspace screen (request editor + response)

Scope: the main app screen in web/. Mode: Operate.
Audience and job: developers and testers composing, sending and reading API requests all day; later the same shell hosts runs and load tests.
Constraints: standard web controls; keyboard-first; WCAG 2.2 AA; dark only for now (scene below).
Memorable moment: the response time and the timing breakdown glowing on the heat scale.
Unresolved: light theme; user-configurable heat thresholds (defaults hard-coded for now).

## Direction contract

THESIS: Latency is heat. Pyro reads timings the way a kiln operator reads metal, by its glow. It refuses the category default of a grey editor with green/red status chips and one blue accent.

OWN-WORLD: Cold iron ground (cool near-black), warm kiln-brick shell (top bar and rail). One heat ramp, used only on measured data: cold steel, ember, cherry, orange, yellow, white-hot, always with a visible legend. Orange is the only accent for chrome (primary action, selection). Errors are crimson with an icon, never left to colour alone. Archivo for UI and tabular numerals, JetBrains Mono for URLs, bodies and code. Dense 28px rows, 1px rules, small radii.

STORY: Pick a request, edit, send. Within one glance the user sees status, how hot the time ran and which phase made it hot, then reads the body.

FIRST VIEWPORT: Brick top bar: wordmark, workspace, environment select and Import on the right. Brick rail on the left: Collections/History tabs and tree. Main area: request name and path, then the URL bar (method select, URL input, orange Send). Below it, tabs for Params/Headers/Body/Auth with key-value tables. The lower split holds the response: status, heat-coloured time, size, then tabs Body/Headers/Cookies/Timing. The Timing tab shows the heated-bar waterfall and its legend.

FORM: Pyrometric incandescence scale, position 6 of 7 on the grounded list; seed:ba8dd3827cf570ed. Raises: colour quarantined to data plates (from lexicon plates); stated legend for every heat colour (from orienteering ISOM legend); a breach stays lit until noticed (from gate board); dense whole-row packing, no cards (from convention catalog); tabular numerals at data density (from datamatics).

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
