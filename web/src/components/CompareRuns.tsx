import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowLeftRight,
  Check,
  Equal,
  Minus,
  Table2,
  TrendingDown,
  TrendingUp,
  X,
} from "lucide-react";
import { api, type LoadResult, type LoadTick, type RequestStat } from "../api";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";
import { LineChart, clock, fmtValue, type Series } from "./LineChart";

// Two saved runs side by side: summary deltas, overlaid charts and a
// per-request table. The baseline is the earlier run unless swapped.

type Better = "lower" | "higher" | "none";

interface Row {
  label: string;
  a: number;
  b: number;
  fmt: (v: number) => string;
  better: Better;
}

/** Changes smaller than this (relative) count as no change. */
const NOISE = 0.02;

export function CompareRunsPage({ a, b }: { a: string; b: string }) {
  const select = useApp((s) => s.select);
  const [showTable, setShowTable] = useState(false);
  const qa = useQuery({
    queryKey: ["loadrun", a],
    queryFn: () => api.loadRun(a),
  });
  const qb = useQuery({
    queryKey: ["loadrun", b],
    queryFn: () => api.loadRun(b),
  });
  if (qa.error || qb.error)
    return (
      <p className="pad inline-error">{(qa.error ?? qb.error)!.message}</p>
    );
  if (!qa.data || !qb.data) return <p className="pad muted">Loading…</p>;
  const ra = qa.data.result;
  const rb = qb.data.result;
  if (!ra || !rb)
    return (
      <p className="pad muted">Both runs must have finished to compare them.</p>
    );

  const m = (r: LoadResult, k: string) => r.metrics[k];
  const dur = (r: LoadResult) =>
    m(r, "http_req_duration") ?? { avg: 0, med: 0, p95: 0, p99: 0, max: 0 };
  const failed = (r: LoadResult) => m(r, "http_req_failed")?.value ?? 0;
  const rows: Row[] = [
    {
      label: "Requests per second",
      a: m(ra, "http_reqs")?.perSecond ?? 0,
      b: m(rb, "http_reqs")?.perSecond ?? 0,
      fmt: (v) => fmtValue(v, ""),
      better: "higher",
    },
    {
      label: "Requests",
      a: m(ra, "http_reqs")?.value ?? 0,
      b: m(rb, "http_reqs")?.value ?? 0,
      fmt: (v) => Math.round(v).toLocaleString(),
      better: "none",
    },
    {
      label: "Failed requests",
      a: failed(ra),
      b: failed(rb),
      fmt: (v) => `${(v * 100).toFixed(2)}%`,
      better: "lower",
    },
    {
      label: "Median (p50)",
      a: dur(ra).med ?? 0,
      b: dur(rb).med ?? 0,
      fmt: formatMs,
      better: "lower",
    },
    {
      label: "p95",
      a: dur(ra).p95 ?? 0,
      b: dur(rb).p95 ?? 0,
      fmt: formatMs,
      better: "lower",
    },
    {
      label: "p99",
      a: dur(ra).p99 ?? 0,
      b: dur(rb).p99 ?? 0,
      fmt: formatMs,
      better: "lower",
    },
    {
      label: "Max",
      a: dur(ra).max ?? 0,
      b: dur(rb).max ?? 0,
      fmt: formatMs,
      better: "lower",
    },
    {
      label: "Max VUs",
      a: ra.maxVUs,
      b: rb.maxVUs,
      fmt: (v) => String(v),
      better: "none",
    },
    {
      label: "Duration",
      a: ra.duration,
      b: rb.duration,
      fmt: clock,
      better: "none",
    },
  ];

  const ta = ra.ticks ?? [];
  const tb = rb.ticks ?? [];
  const longer = ta.length >= tb.length ? ta : tb;
  const x = longer.map((t) => t.elapsed);
  const pad = (ticks: LoadTick[], f: (t: LoadTick) => number) =>
    longer.map((_, i) => (i < ticks.length ? f(ticks[i]) : NaN));
  const pair = (f: (t: LoadTick) => number): Series[] => [
    { name: "Baseline", color: "var(--viz-base)", values: pad(ta, f) },
    { name: "Candidate", color: "var(--viz-series)", values: pad(tb, f) },
  ];
  const hasHTTP =
    (m(ra, "http_reqs")?.value ?? 0) + (m(rb, "http_reqs")?.value ?? 0) > 0;

  return (
    <section className="loadrun compare">
      <header className="lt-head">
        <div>
          <h1>Compare runs</h1>
          <p className="muted small">
            {ra.name}
            {ra.name !== rb.name && <> vs {rb.name}</>}
          </p>
        </div>
        <div className="lt-actions">
          <button
            className="btn ghost"
            onClick={() => select(`compare:${b}:${a}`)}
            title="Make the candidate the baseline"
          >
            <ArrowLeftRight size={13} /> Swap
          </button>
        </div>
      </header>

      <div className="compare-runs">
        <RunCard role="Baseline" swatch="var(--viz-base)" id={a} r={ra} />
        <RunCard role="Candidate" swatch="var(--viz-series)" id={b} r={rb} />
      </div>

      <div className="scroll-x">
        <table className="data-table compare-table">
          <thead>
            <tr>
              <th>Metric</th>
              <th className="num">Baseline</th>
              <th className="num">Candidate</th>
              <th>Change</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.label}>
                <td>{r.label}</td>
                <td className="num">{r.fmt(r.a)}</td>
                <td className="num">{r.fmt(r.b)}</td>
                <td>
                  <Delta a={r.a} b={r.b} better={r.better} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ThresholdCompare a={ra} b={rb} />

      <div className="chart-head">
        <h2 className="sub-head">Over time</h2>
        <button
          className="btn ghost small"
          onClick={() => setShowTable(!showTable)}
          aria-pressed={showTable}
        >
          <Table2 size={13} /> {showTable ? "Charts" : "Table view"}
        </button>
      </div>
      {showTable ? (
        <CompareTickTable ta={ta} tb={tb} />
      ) : (
        <div className="chart-grid">
          {hasHTTP && (
            <LineChart
              title="p95 response time"
              x={x}
              series={pair((t) => t.latency.p95)}
              unit="ms"
            />
          )}
          {hasHTTP && (
            <LineChart
              title="Requests per second"
              x={x}
              series={pair((t) => t.rps)}
              unit=""
            />
          )}
          <LineChart
            title="Active virtual users"
            x={x}
            series={pair((t) => t.vus)}
            unit=""
          />
          {hasHTTP && (
            <LineChart
              title="Failed requests"
              x={x}
              series={pair((t) => t.errorRate)}
              unit="%"
            />
          )}
        </div>
      )}

      <RequestCompare a={ra.requests ?? []} b={rb.requests ?? []} />
    </section>
  );
}

function RunCard({
  role,
  swatch,
  id,
  r,
}: {
  role: string;
  swatch: string;
  id: string;
  r: LoadResult;
}) {
  const select = useApp((s) => s.select);
  return (
    <button className="run-card" onClick={() => select(`loadrun:${id}`)}>
      <span className="run-card-role">
        <i style={{ background: swatch }} aria-hidden /> {role}
      </span>
      <span>{new Date(r.startedAt).toLocaleString()}</span>
      <span className="muted small">
        <span className={`verdict v-${r.status}`}>
          {r.status[0].toUpperCase() + r.status.slice(1)}
        </span>{" "}
        · {clock(r.duration)} · up to {r.maxVUs} VUs
        {r.agents?.length ? ` · ${r.agents.length} agents` : ""}
      </span>
    </button>
  );
}

/** Relative change from a to b, judged by which direction is better. */
function Delta({ a, b, better }: { a: number; b: number; better: Better }) {
  if (a === 0 && b === 0) return <span className="delta">–</span>;
  const rel = a === 0 ? Infinity : (b - a) / a;
  const pct = Number.isFinite(rel)
    ? `${rel > 0 ? "+" : ""}${(rel * 100).toFixed(Math.abs(rel) < 0.1 ? 1 : 0)}%`
    : "new";
  if (Math.abs(rel) < NOISE) {
    return (
      <span className="delta">
        <Equal size={13} aria-hidden /> {pct} · no change
      </span>
    );
  }
  const up = b > a;
  const Icon = up ? TrendingUp : TrendingDown;
  if (better === "none") {
    return (
      <span className="delta">
        <Icon size={13} aria-hidden /> {pct}
      </span>
    );
  }
  const good = (better === "lower") !== up;
  return (
    <span className={`delta ${good ? "is-better" : "is-worse"}`}>
      <Icon size={13} aria-hidden /> {pct} · {good ? "better" : "worse"}
    </span>
  );
}

function ThresholdCompare({ a, b }: { a: LoadResult; b: LoadResult }) {
  const keys = new Map<
    string,
    { a?: boolean; b?: boolean; noA?: boolean; noB?: boolean }
  >();
  for (const t of a.thresholds ?? [])
    keys.set(`${t.metric} ${t.condition}`, { a: t.passed, noA: t.noData });
  for (const t of b.thresholds ?? []) {
    const k = `${t.metric} ${t.condition}`;
    keys.set(k, { ...keys.get(k), b: t.passed, noB: t.noData });
  }
  if (keys.size === 0) return null;
  const mark = (passed?: boolean, noData?: boolean) =>
    passed === undefined ? (
      <span className="muted">not set</span>
    ) : noData ? (
      <span className="th-mark">
        <Minus size={13} aria-hidden /> no data
      </span>
    ) : passed ? (
      <span className="th-mark is-pass">
        <Check size={13} aria-hidden /> passed
      </span>
    ) : (
      <span className="th-mark is-fail">
        <X size={13} aria-hidden /> failed
      </span>
    );
  return (
    <>
      <h2 className="sub-head">Thresholds</h2>
      <div className="scroll-x">
        <table className="data-table compare-table">
          <thead>
            <tr>
              <th>Threshold</th>
              <th>Baseline</th>
              <th>Candidate</th>
            </tr>
          </thead>
          <tbody>
            {[...keys].map(([k, v]) => (
              <tr key={k}>
                <td className="mono">{k}</td>
                <td>{mark(v.a, v.noA)}</td>
                <td>{mark(v.b, v.noB)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function RequestCompare({ a, b }: { a: RequestStat[]; b: RequestStat[] }) {
  const key = (r: RequestStat) => `${r.scenario}\u0000${r.request}`;
  const byKey = new Map<string, { a?: RequestStat; b?: RequestStat }>();
  for (const r of a) byKey.set(key(r), { a: r });
  for (const r of b) byKey.set(key(r), { ...byKey.get(key(r)), b: r });
  if (byKey.size === 0) return null;
  const rows = [...byKey.values()].sort((x, y) => {
    const rx = x.a ?? x.b!;
    const ry = y.a ?? y.b!;
    return (
      rx.scenario.localeCompare(ry.scenario) ||
      rx.request.localeCompare(ry.request)
    );
  });
  return (
    <>
      <h2 className="sub-head">Requests</h2>
      <div className="scroll-x">
        <table className="data-table compare-table">
          <thead>
            <tr>
              <th>Request</th>
              <th>Scenario</th>
              <th className="num">Count</th>
              <th className="num">Failed</th>
              <th className="num">p95 baseline</th>
              <th className="num">p95 candidate</th>
              <th>p95 change</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ a: ra, b: rb }) => {
              const r = (ra ?? rb)!;
              return (
                <tr key={key(r)}>
                  <td className="mono">
                    {r.method} {r.request}
                  </td>
                  <td>{r.scenario}</td>
                  <td className="num">
                    {(ra?.count ?? 0).toLocaleString()} →{" "}
                    {(rb?.count ?? 0).toLocaleString()}
                  </td>
                  <td className="num">
                    {(ra?.failed ?? 0).toLocaleString()} →{" "}
                    {(rb?.failed ?? 0).toLocaleString()}
                  </td>
                  <HeatCell ms={ra?.duration.p95} />
                  <HeatCell ms={rb?.duration.p95} />
                  <td>
                    {ra && rb ? (
                      <Delta
                        a={ra.duration.p95 ?? 0}
                        b={rb.duration.p95 ?? 0}
                        better="lower"
                      />
                    ) : (
                      <span className="muted">only in one run</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

function HeatCell({ ms }: { ms?: number }) {
  if (ms === undefined) return <td className="num muted">–</td>;
  return (
    <td className="num">
      <span className={`swatch heat-${heatOf(ms)}`} aria-hidden />
      {formatMs(ms)}
    </td>
  );
}

function CompareTickTable({ ta, tb }: { ta: LoadTick[]; tb: LoadTick[] }) {
  const n = Math.max(ta.length, tb.length);
  const cell = (t: LoadTick | undefined, f: (t: LoadTick) => string) => (
    <td className="num">{t ? f(t) : "–"}</td>
  );
  return (
    <div className="scroll-x tick-table">
      <table className="data-table">
        <thead>
          <tr>
            <th>Time</th>
            <th className="num">p95 baseline</th>
            <th className="num">p95 candidate</th>
            <th className="num">req/s baseline</th>
            <th className="num">req/s candidate</th>
            <th className="num">Failed baseline</th>
            <th className="num">Failed candidate</th>
          </tr>
        </thead>
        <tbody>
          {Array.from({ length: n }, (_, i) => (
            <tr key={i}>
              <td className="mono">{clock((ta[i] ?? tb[i]).elapsed)}</td>
              {cell(ta[i], (t) => formatMs(t.latency.p95))}
              {cell(tb[i], (t) => formatMs(t.latency.p95))}
              {cell(ta[i], (t) => t.rps.toFixed(1))}
              {cell(tb[i], (t) => t.rps.toFixed(1))}
              {cell(ta[i], (t) => `${(t.errorRate * 100).toFixed(2)}%`)}
              {cell(tb[i], (t) => `${(t.errorRate * 100).toFixed(2)}%`)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
