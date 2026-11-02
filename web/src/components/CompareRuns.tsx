import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Table, Tag, type TableColumnsType } from "antd";
import {
  ArrowLeftRight,
  Check,
  Equal,
  Minus,
  Table2,
  TrendingDown,
  TrendingUp,
  X,
} from "../icons";
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
      <div className="page">
        <Alert type="error" showIcon title={(qa.error ?? qb.error)!.message} />
      </div>
    );
  if (!qa.data || !qb.data) return <p className="pad muted">Loading…</p>;
  const ra = qa.data.result;
  const rb = qb.data.result;
  if (!ra || !rb)
    return (
      <div className="page">
        <Alert type="info" showIcon title="Both runs must have finished to compare them." />
      </div>
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
    <div className="page compare">
      <div className="dash">
        <header className="lt-head dash-head">
          <div>
            <h1>Compare runs</h1>
            <p className="muted small">
              {ra.name}
              {ra.name !== rb.name && <> vs {rb.name}</>}
            </p>
          </div>
          <div className="lt-actions">
            <Button icon={<ArrowLeftRight size={13} />} onClick={() => select(`compare:${b}:${a}`)} title="Make the candidate the baseline">
              Swap
            </Button>
          </div>
        </header>

        <div className="compare-runs">
          <RunCard role="Baseline" swatch="var(--viz-base)" id={a} r={ra} />
          <RunCard role="Candidate" swatch="var(--viz-series)" id={b} r={rb} />
        </div>

        <section className="card dash-card">
          <h2 className="dash-title">Summary</h2>
          <Table<Row>
            className="compare-table"
            size="small"
            pagination={false}
            scroll={{ x: "max-content" }}
            rowKey="label"
            dataSource={rows}
            columns={[
              { title: "Metric", dataIndex: "label" },
              { title: "Baseline", key: "a", align: "right", render: (_, r) => r.fmt(r.a) },
              { title: "Candidate", key: "b", align: "right", render: (_, r) => r.fmt(r.b) },
              { title: "Change", key: "delta", render: (_, r) => <Delta a={r.a} b={r.b} better={r.better} /> },
            ]}
          />
        </section>

        <ThresholdCompare a={ra} b={rb} />

        <div className="chart-head">
          <h2 className="dash-title">Over time</h2>
          <Button size="small" icon={<Table2 size={13} />} onClick={() => setShowTable(!showTable)} aria-pressed={showTable}>
            {showTable ? "Charts" : "Table view"}
          </Button>
        </div>
        {showTable ? (
          <section className="card dash-card">
            <CompareTickTable ta={ta} tb={tb} />
          </section>
        ) : (
          <div className="chart-grid">
            {hasHTTP && <LineChart title="p95 response time" x={x} series={pair((t) => t.latency.p95)} unit="ms" />}
            {hasHTTP && <LineChart title="Requests per second" x={x} series={pair((t) => t.rps)} unit="" />}
            <LineChart title="Active virtual users" x={x} series={pair((t) => t.vus)} unit="" />
            {hasHTTP && <LineChart title="Failed requests" x={x} series={pair((t) => t.errorRate)} unit="%" />}
          </div>
        )}

        <RequestCompare a={ra.requests ?? []} b={rb.requests ?? []} />
      </div>
    </div>
  );
}

const STATUS_COLOR: Record<string, string> = { passed: "success", failed: "error", aborted: "error", stopped: "warning" };

function RunCard({ role, swatch, id, r }: { role: string; swatch: string; id: string; r: LoadResult }) {
  const select = useApp((s) => s.select);
  // The whole card opens the run; a native button keeps it one tab stop.
  return (
    <button className="card run-card" onClick={() => select(`loadrun:${id}`)}>
      <span className="run-card-role">
        <i style={{ background: swatch }} aria-hidden /> {role}
        <Tag className={`verdict v-${r.status}`} color={STATUS_COLOR[r.status]}>
          {r.status[0].toUpperCase() + r.status.slice(1)}
        </Tag>
      </span>
      <span>{new Date(r.startedAt).toLocaleString()}</span>
      <span className="muted small">
        {clock(r.duration)} · up to {r.maxVUs} VUs
        {r.agents?.length ? ` · ${r.agents.length} agents` : ""}
      </span>
    </button>
  );
}

/** Relative change from a to b, judged by which direction is better. */
function Delta({ a, b, better }: { a: number; b: number; better: Better }) {
  if (a === 0 && b === 0) return <span className="delta">–</span>;
  const rel = a === 0 ? Infinity : (b - a) / a;
  const pct = Number.isFinite(rel) ? `${rel > 0 ? "+" : ""}${(rel * 100).toFixed(Math.abs(rel) < 0.1 ? 1 : 0)}%` : "new";
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
  const keys = new Map<string, { a?: boolean; b?: boolean; noA?: boolean; noB?: boolean }>();
  for (const t of a.thresholds ?? []) keys.set(`${t.metric} ${t.condition}`, { a: t.passed, noA: t.noData });
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
  const data = [...keys].map(([key, v]) => ({ key, ...v }));
  return (
    <section className="card dash-card">
      <h2 className="dash-title">Thresholds</h2>
      <Table
        className="compare-table"
        size="small"
        pagination={false}
        scroll={{ x: "max-content" }}
        dataSource={data}
        columns={[
          { title: "Threshold", dataIndex: "key", className: "mono" },
          { title: "Baseline", key: "a", render: (_, v) => mark(v.a, v.noA) },
          { title: "Candidate", key: "b", render: (_, v) => mark(v.b, v.noB) },
        ]}
      />
    </section>
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
    return rx.scenario.localeCompare(ry.scenario) || rx.request.localeCompare(ry.request);
  });
  type Pair = { a?: RequestStat; b?: RequestStat };
  const one = (p: Pair) => (p.a ?? p.b)!;
  const columns: TableColumnsType<Pair> = [
    { title: "Request", key: "request", className: "mono", render: (_, p) => `${one(p).method} ${one(p).request}` },
    { title: "Scenario", key: "scenario", render: (_, p) => one(p).scenario },
    { title: "Count", key: "count", align: "right", render: (_, p) => `${(p.a?.count ?? 0).toLocaleString()} → ${(p.b?.count ?? 0).toLocaleString()}` },
    { title: "Failed", key: "failed", align: "right", render: (_, p) => `${(p.a?.failed ?? 0).toLocaleString()} → ${(p.b?.failed ?? 0).toLocaleString()}` },
    { title: "p95 baseline", key: "p95a", align: "right", render: (_, p) => <HeatValue ms={p.a?.duration.p95} /> },
    { title: "p95 candidate", key: "p95b", align: "right", render: (_, p) => <HeatValue ms={p.b?.duration.p95} /> },
    {
      title: "p95 change",
      key: "delta",
      render: (_, p) => (p.a && p.b ? <Delta a={p.a.duration.p95 ?? 0} b={p.b.duration.p95 ?? 0} better="lower" /> : <span className="muted">only in one run</span>),
    },
  ];
  return (
    <section className="card dash-card">
      <h2 className="dash-title">Requests</h2>
      <Table<Pair> className="compare-table" size="small" pagination={false} scroll={{ x: "max-content" }} rowKey={(p) => key(one(p))} dataSource={rows} columns={columns} />
    </section>
  );
}

/** A duration with its heat swatch beside it (text stays neutral). */
function HeatValue({ ms }: { ms?: number }) {
  if (ms === undefined) return <span className="muted">–</span>;
  return (
    <span className="heat-value">
      <span className={`swatch heat-${heatOf(ms)}`} aria-hidden />
      {formatMs(ms)}
    </span>
  );
}

function CompareTickTable({ ta, tb }: { ta: LoadTick[]; tb: LoadTick[] }) {
  const n = Math.max(ta.length, tb.length);
  const data = Array.from({ length: n }, (_, i) => ({ i, a: ta[i] as LoadTick | undefined, b: tb[i] as LoadTick | undefined }));
  type R = (typeof data)[number];
  const cell = (t: LoadTick | undefined, f: (t: LoadTick) => string) => (t ? f(t) : "–");
  const p95 = (t: LoadTick) => formatMs(t.latency.p95);
  const rps = (t: LoadTick) => t.rps.toFixed(1);
  const failed = (t: LoadTick) => `${(t.errorRate * 100).toFixed(2)}%`;
  return (
    <Table<R>
      className="tick-table"
      size="small"
      pagination={false}
      scroll={{ x: "max-content", y: 420 }}
      rowKey="i"
      dataSource={data}
      columns={[
        { title: "Time", key: "time", className: "mono", render: (_, r) => clock((r.a ?? r.b)!.elapsed) },
        { title: "p95 baseline", key: "p95a", align: "right", render: (_, r) => cell(r.a, p95) },
        { title: "p95 candidate", key: "p95b", align: "right", render: (_, r) => cell(r.b, p95) },
        { title: "req/s baseline", key: "rpsa", align: "right", render: (_, r) => cell(r.a, rps) },
        { title: "req/s candidate", key: "rpsb", align: "right", render: (_, r) => cell(r.b, rps) },
        { title: "Failed baseline", key: "fa", align: "right", render: (_, r) => cell(r.a, failed) },
        { title: "Failed candidate", key: "fb", align: "right", render: (_, r) => cell(r.b, failed) },
      ]}
    />
  );
}
