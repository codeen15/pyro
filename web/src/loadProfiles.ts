// Ready-made load profiles. Each generates a commented *.load.yaml so the
// file explains itself when opened in an editor or reviewed in git.

export type ProfileId = "smoke" | "load" | "stress" | "spike" | "soak" | "breakpoint";

export interface Profile {
  id: ProfileId;
  name: string;
  summary: string;
  /** What the target number means for this profile. */
  targetLabel: string;
  defaultTarget: number;
}

export const PROFILES: Profile[] = [
  { id: "smoke", name: "Smoke", summary: "1–2 VUs for a minute. Checks the flow works before anything heavier.", targetLabel: "VUs", defaultTarget: 2 },
  { id: "load", name: "Load", summary: "Ramp to your expected peak, hold it, ramp down.", targetLabel: "Peak VUs", defaultTarget: 50 },
  { id: "stress", name: "Stress", summary: "Climb past the expected peak in steps to see where it degrades.", targetLabel: "Expected peak VUs", defaultTarget: 100 },
  { id: "spike", name: "Spike", summary: "A sudden jump from a small baseline, then back down.", targetLabel: "Spike VUs", defaultTarget: 200 },
  { id: "soak", name: "Soak", summary: "A moderate level for an hour to surface leaks and slow drift.", targetLabel: "VUs", defaultTarget: 30 },
  {
    id: "breakpoint",
    name: "Breakpoint",
    summary: "Raise the request rate until thresholds fail, then stop.",
    targetLabel: "Max iterations / s",
    defaultTarget: 500,
  },
];

const q = (s: string) => JSON.stringify(s);

export function generatePlan(opts: { title: string; requests: string[]; profile: ProfileId; target: number }): string {
  const n = Math.max(1, Math.round(opts.target));
  const flow = opts.requests.flatMap((r) => [`      - request: ${r}`, `      - think: { min: 500ms, max: 1500ms }`]).join("\n");
  const head = `name: ${q(opts.title)}\n`;
  const th = (p95: number, failRate: string, abort = false) =>
    abort
      ? `thresholds:
  http_req_duration:
    - { threshold: "p(95)<${p95}", abortOnFail: true, delayAbortEval: 30s }
  http_req_failed:
    - { threshold: "rate<${failRate}", abortOnFail: true, delayAbortEval: 30s }
`
      : `thresholds:
  http_req_duration: ["p(95)<${p95}"]
  http_req_failed: ["rate<${failRate}"]
`;
  switch (opts.profile) {
    case "smoke":
      return `${head}# Smoke: a couple of users for a minute to check the flow works.
scenarios:
  smoke:
    executor: constant-vus
    vus: ${n}
    duration: 1m
    flow:
${flow}
${th(1000, "0.01")}`;
    case "load":
      return `${head}# Load: ramp to the expected peak, hold it, ramp down.
scenarios:
  load:
    executor: ramping-vus
    stages:
      - { duration: 2m, target: ${n} }
      - { duration: 5m, target: ${n} }
      - { duration: 1m, target: 0 }
    flow:
${flow}
${th(500, "0.01")}`;
    case "stress":
      return `${head}# Stress: step past the expected peak to find where it degrades.
scenarios:
  stress:
    executor: ramping-vus
    stages:
      - { duration: 2m, target: ${n} }
      - { duration: 3m, target: ${n} }
      - { duration: 2m, target: ${Math.round(n * 1.5)} }
      - { duration: 3m, target: ${Math.round(n * 1.5)} }
      - { duration: 2m, target: ${n * 2} }
      - { duration: 3m, target: ${n * 2} }
      - { duration: 2m, target: 0 }
    flow:
${flow}
${th(1000, "0.05")}`;
    case "spike": {
      const base = Math.max(1, Math.round(n / 10));
      return `${head}# Spike: a small baseline, a sudden jump, then recovery.
scenarios:
  spike:
    executor: ramping-vus
    startVUs: ${base}
    stages:
      - { duration: 30s, target: ${base} }
      - { duration: 10s, target: ${n} }
      - { duration: 1m, target: ${n} }
      - { duration: 10s, target: ${base} }
      - { duration: 1m, target: ${base} }
      - { duration: 10s, target: 0 }
    flow:
${flow}
${th(1500, "0.05")}`;
    }
    case "soak":
      return `${head}# Soak: a moderate level for an hour to surface leaks and drift.
scenarios:
  soak:
    executor: ramping-vus
    stages:
      - { duration: 2m, target: ${n} }
      - { duration: 1h, target: ${n} }
      - { duration: 2m, target: 0 }
    flow:
${flow}
${th(500, "0.01")}`;
    case "breakpoint":
      return `${head}# Breakpoint: raise the arrival rate until a threshold fails, then stop.
scenarios:
  breakpoint:
    executor: ramping-arrival-rate
    startRate: ${Math.max(1, Math.round(n / 50))}
    timeUnit: 1s
    preAllocatedVUs: ${Math.max(10, Math.round(n / 5))}
    maxVUs: ${Math.max(50, n * 2)}
    stages:
      - { duration: 10m, target: ${n} }
    flow:
${flow
  .split("\n")
  .filter((l) => !l.includes("think"))
  .join("\n")}
${th(1000, "0.05", true)}`;
  }
}

/** File-system-safe load test name from a title. */
export function slugify(s: string) {
  return (
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 60) || "load-test"
  );
}
