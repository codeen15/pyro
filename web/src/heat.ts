// The pyrometric heat scale: measured durations glow from cold steel to
// white heat. Colours live in styles.css as --heat-<step>; this module only
// decides which step a value falls on.
// TODO: make thresholds configurable per workspace.

export type HeatStep = "cold" | "ember" | "cherry" | "orange" | "yellow" | "white";

/** Upper bounds (ms, exclusive) for each step; anything above the last is white-hot. */
export const HEAT_STEPS: { step: HeatStep; below: number; label: string }[] = [
  { step: "cold", below: 100, label: "< 100 ms" },
  { step: "ember", below: 300, label: "100–300" },
  { step: "cherry", below: 600, label: "300–600" },
  { step: "orange", below: 1000, label: "600 ms–1 s" },
  { step: "yellow", below: 2000, label: "1–2 s" },
  { step: "white", below: Infinity, label: "> 2 s" },
];

export function heatOf(ms: number): HeatStep {
  return HEAT_STEPS.find((s) => ms < s.below)!.step;
}

export function formatMs(ms: number): string {
  if (ms >= 10_000) return `${(ms / 1000).toFixed(1)} s`;
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms >= 10) return `${Math.round(ms)} ms`;
  return `${ms.toFixed(1)} ms`;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
