import { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Table, Tag, type TableColumnsType } from "antd";
import { Check, Download, ExternalLink, Minus, Network, Play, Square, Table2, TriangleAlert, X } from "../icons";
import { api, type AgentStatus, type ErrorCount, type LiveSnapshot, type LoadRunView, type LoadTick, type RequestStat, type ThresholdResult } from "../api";
import { subscribe } from "../events";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";
import { LineChart, clock, fmtValue, type Ref } from "./LineChart";

const VERDICT: Record<string, { label: string; icon: typeof Check; color: string }> = {
  passed: { label: "Passed", icon: Check, color: "success" },
  failed: { label: "Failed", icon: X, color: "error" },
  aborted: { label: "Aborted", icon: X, color: "error" },
  stopped: { label: "Stopped", icon: Square, color: "warning" },
};

interface State {
  status: LoadRunView["status"];
  test: string;
  name?: string;
  reason?: string;
  ticks: LoadTick[];
  requests: RequestStat[];
  errors: ErrorCount[];
  thresholds: ThresholdResult[];
  totals?: Record<string, { value: number; perSecond?: number; p95?: number }>;
  maxVUs?: number;
  maxRPS?: number;
  agents?: AgentStatus[];
  warnings?: string[];
}

export function LoadRunPage({ id }: { id: string }) {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const [st, setSt] = useState<State | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showTable, setShowTable] = useState(false);
  const [stopping, setStopping] = useState(false);
  // Breached thresholds stay lit until the user acknowledges them.
  const [seen, setSeen] = useState<Set<string>>(new Set());

  useEffect(() => {
    const fromView = (v: LoadRunView): State => {
      if (v.result) {
        const r = v.result;
        return {
          status: r.status,
          test: v.test,
          name: r.name,
          reason: r.reason,
          ticks: r.ticks ?? [],
          requests: r.requests ?? [],
          errors: r.errors ?? [],
          thresholds: r.thresholds ?? [],
          totals: r.metrics,
          maxVUs: r.maxVUs,
          agents: r.agents,
          warnings: r.warnings,
          maxRPS: r.maxRPS,
        };
      }
      return {
        status: "running",
        test: v.test,
        name: v.plan?.name,
        ticks: v.ticks ?? [],
        requests: v.live?.requests ?? [],
        errors: v.live?.errors ?? [],
        thresholds: v.live?.tick.thresholds ?? [],
      };
    };
    // Subscribe first, then fetch; ticks merge by elapsed time.
    const off = subscribe<LiveSnapshot | LoadRunView>(`load:${id}`, (e) => {
      if (e.type === "tick") {
        const s = e.data as LiveSnapshot;
        setSt((cur) => {
          const base = cur ?? { status: "running" as const, test: "", ticks: [], requests: [], errors: [], thresholds: [] };
          if (base.status !== "running" || base.ticks.some((t) => t.elapsed === s.tick.elapsed)) return base;
          return { ...base, ticks: [...base.ticks, s.tick], requests: s.requests ?? [], errors: s.errors ?? [], thresholds: s.tick.thresholds ?? [] };
        });
      }
      if (e.type === "finished") {
        setSt(fromView(e.data as LoadRunView));
        setStopping(false);
        qc.invalidateQueries({ queryKey: ["loadruns"] });
      }
    });
    api.loadRun(id).then(
      (v) =>
        setSt((cur) => {
          const fresh = fromView(v);
          if (!cur || fresh.status !== "running") return fresh;
          const known = new Set(fresh.ticks.map((t) => t.elapsed));
          return { ...fresh, ticks: [...fresh.ticks, ...cur.ticks.filter((t) => !known.has(t.elapsed))].sort((a, b) => a.elapsed - b.elapsed) };
        }),
      (e) => setError((e as Error).message),
    );
    return off;
  }, [id, qc]);

  const x = useMemo(() => st?.ticks.map((t) => t.elapsed) ?? [], [st]);
  const charts = useMemo(() => {
    const t = st?.ticks ?? [];
    return {
      rps: [{ name: "req/s", color: "var(--viz-series)", values: t.map((k) => k.rps) }],
      vus: [{ name: "VUs", color: "var(--viz-series)", values: t.map((k) => k.vus) }],
      latency: [
        { name: "p50", color: "var(--viz-p50)", values: t.map((k) => k.latency.p50) },
        { name: "p95", color: "var(--viz-p95)", values: t.map((k) => k.latency.p95) },
        { name: "p99", color: "var(--viz-p99)", values: t.map((k) => k.latency.p99) },
      ],
      errors: [{ name: "failed", color: "var(--viz-series)", values: t.map((k) => k.errorRate) }],
      ws: t.some((k) => (k.wsMessagesPerSecond ?? 0) > 0) ? [{ name: "msgs/s", color: "var(--viz-series)", values: t.map((k) => k.wsMessagesPerSecond ?? 0) }] : null,
    };
  }, [st]);

  if (error)
    return (
      <div className="page">
        <Alert type="error" showIcon title={error} />
      </div>
    );
  if (!st) return <p className="pad muted">Loading…</p>;

  const running = st.status === "running";
  const last = st.ticks[st.ticks.length - 1];
  const agents = running ? last?.agents : (st.agents ?? last?.agents);
  const totalReqs = st.requests.reduce((n, r) => n + r.count, 0);
  const totalFailed = st.requests.reduce((n, r) => n + r.failed, 0);
  const p95 = running ? last?.latency.p95 ?? 0 : st.totals?.http_req_duration?.p95 ?? 0;
  const hasHTTP = totalReqs > 0 || (last?.rps ?? 0) > 0;
  const refs: Ref[] = st.thresholds
    .filter((t) => t.metric === "http_req_duration")
    .flatMap((t) => {
      const m = /^p\((\d+(?:\.\d+)?)\)\s*<=?\s*(\d+(?:\.\d+)?)$/.exec(t.condition.replace(/\s/g, ""));
      return m ? [{ label: t.condition, value: Number(m[2]) }] : [];
    });
  const v = VERDICT[st.status];
  const [p95Value, p95Unit] = formatMs(p95).split(" ");

  const openReport = async (download: boolean) => {
    const html = await api.loadReport(id);
    const url = URL.createObjectURL(new Blob([html], { type: "text/html" }));
    if (download) {
      const a = document.createElement("a");
      a.href = url;
      a.download = `${st.test || "load"}-report.html`;
      a.click();
    } else {
      window.open(url, "_blank", "noopener");
    }
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  };

  return (
    <div className="page loadrun">
      <div className="dash">
        <header className="lt-head dash-head">
          <div>
            <h1>
              {st.name || st.test || "Load test"}
              {running ? (
                <Tag className="verdict v-running" color="processing">
                  Running · {clock(last?.elapsed ?? 0)}
                </Tag>
              ) : (
                v && (
                  <Tag className={`verdict v-${st.status}`} color={v.color} icon={<v.icon size={12} />}>
                    {v.label}
                  </Tag>
                )
              )}
            </h1>
            <p className="muted small">
              {st.test && (
                <Button type="link" className="inline-link" onClick={() => select(`load:${st.test}`)}>
                  loadtests/{st.test}.load.yaml
                </Button>
              )}
              {st.maxRPS ? <> · capped at {st.maxRPS} req/s</> : null}
              {st.reason && <> · {st.reason}</>}
            </p>
          </div>
          <div className="lt-actions">
            {running ? (
              <>
                <Button
                  icon={<Square size={13} />}
                  disabled={stopping}
                  onClick={() => {
                    setStopping(true);
                    api.stopLoadRun(id);
                  }}
                  title="No new iterations; in-flight ones finish"
                >
                  {stopping ? "Stopping…" : "Stop"}
                </Button>
                {stopping && (
                  <Button danger onClick={() => api.stopLoadRun(id, true)} title="Cancel in-flight requests now">
                    Abort now
                  </Button>
                )}
              </>
            ) : (
              <>
                <Button icon={<ExternalLink size={13} />} onClick={() => openReport(false)}>
                  Report
                </Button>
                <Button icon={<Download size={13} />} onClick={() => openReport(true)} aria-label="Download report" />
              </>
            )}
          </div>
        </header>

        <div className="kpis">
          {running ? <Kpi label="Virtual users" value={String(last?.vus ?? 0)} /> : <Kpi label="Virtual users" value={String(st.maxVUs ?? 0)} unit="max" />}
          {hasHTTP && <Kpi label="Requests per second" value={running ? fmtValue(last?.rps ?? 0, "") : fmtValue(st.totals?.http_reqs?.perSecond ?? 0, "")} />}
          {hasHTTP && <Kpi label={`p95 response time${running ? " (last second)" : ""}`} value={p95Value} unit={p95Unit} heat={heatOf(p95)} />}
          {hasHTTP && <Kpi label="Requests" value={totalReqs.toLocaleString()} sub={`${totalFailed.toLocaleString()} failed (${totalReqs ? ((totalFailed / totalReqs) * 100).toFixed(2) : "0.00"}%)`} />}
          {charts.ws && <Kpi label="Stream messages per second" value={fmtValue(last?.wsMessagesPerSecond ?? 0, "")} />}
          {(last?.dropped ?? 0) > 0 && <Kpi label="Dropped iterations (last second)" value={String(last?.dropped)} sub="Not enough VUs for the arrival rate" />}
          {running && !agents?.length && (last?.cpu ?? -1) >= 0 && (
            <Kpi
              label="Load generator CPU"
              value={String(Math.round((last?.cpu ?? 0) * 100))}
              unit="%"
              sub={(last?.cpu ?? 0) >= 0.9 ? "Saturated: latencies may include Pyro's own waiting" : `${Math.round(last?.memMB ?? 0)} MB memory`}
            />
          )}
        </div>
        {st.warnings?.map((w) => <Alert key={w} type="warning" showIcon role="note" title={w} />)}

        {st.thresholds.length > 0 && (
          <section className="card dash-card">
            <h2 className="dash-title">Thresholds</h2>
            <ul className="th-list" aria-label="Thresholds">
              {st.thresholds.map((t) => {
                const key = `${t.metric} ${t.condition}`;
                const failing = !t.passed;
                const lit = (failing || t.breached) && !seen.has(key);
                return (
                  <li key={key} className={`th ${t.noData ? "is-nodata" : failing ? "is-fail" : "is-pass"} ${lit ? "is-lit" : ""}`}>
                    {t.noData ? <Minus size={14} aria-label="no data" /> : failing ? <X size={14} aria-label="failing" /> : <Check size={14} aria-label="passing" />}
                    <span className="mono">
                      {t.metric} {t.condition}
                    </span>
                    <span className="muted small">{t.noData ? "no data yet" : `now ${thresholdActual(t)}`}</span>
                    {!failing && t.breached && <span className="muted small">· failed earlier</span>}
                    {lit && (
                      <Button className="th-seen" onClick={() => setSeen(new Set(seen).add(key))}>
                        Seen
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          </section>
        )}

        <div className="chart-head">
          <h2 className="dash-title">Over time</h2>
          <Button icon={<Table2 size={13} />} onClick={() => setShowTable(!showTable)} aria-pressed={showTable}>
            {showTable ? "Charts" : "Table view"}
          </Button>
        </div>
        {showTable ? (
          <section className="card dash-card">
            <TickTable ticks={st.ticks} />
          </section>
        ) : (
          <div className="chart-grid">
            {hasHTTP && <LineChart title="Requests per second" x={x} series={charts.rps} unit="" />}
            {hasHTTP && <LineChart title="Response time percentiles" x={x} series={charts.latency} unit="ms" refs={refs} />}
            <LineChart title="Active virtual users" x={x} series={charts.vus} unit="" />
            {hasHTTP && <LineChart title="Failed requests" x={x} series={charts.errors} unit="%" />}
            {charts.ws && <LineChart title="Stream messages received per second" x={x} series={charts.ws} unit="" />}
          </div>
        )}

        {st.requests.length > 0 && (
          <section className="card dash-card">
            <h2 className="dash-title">Requests{running ? " (so far)" : ""}</h2>
            <Table<RequestStat>
              className="req-table"
              size="small"
              pagination={false}
              scroll={{ x: "max-content" }}
              rowKey={(r) => r.scenario + r.request}
              dataSource={st.requests}
              columns={[
                { title: "Request", key: "request", className: "mono", render: (_, r) => `${r.method} ${r.request}` },
                { title: "Scenario", dataIndex: "scenario" },
                { title: "Count", key: "count", align: "right", render: (_, r) => r.count.toLocaleString() },
                { title: "Failed", key: "failed", align: "right", render: (_, r) => <span className={r.failed ? "fail-text" : undefined}>{r.failed.toLocaleString()}</span> },
                { title: "req/s", key: "rps", align: "right", render: (_, r) => r.perSecond.toFixed(1) },
                { title: "Avg", key: "avg", align: "right", render: (_, r) => formatMs(r.duration.avg ?? 0) },
                { title: "p50", key: "p50", align: "right", render: (_, r) => formatMs(r.duration.med ?? 0) },
                { title: "p95", key: "p95", align: "right", render: (_, r) => <HeatValue ms={r.duration.p95 ?? 0} /> },
                { title: "p99", key: "p99", align: "right", render: (_, r) => <HeatValue ms={r.duration.p99 ?? 0} /> },
                { title: "Max", key: "max", align: "right", render: (_, r) => formatMs(r.duration.max ?? 0) },
              ]}
            />
          </section>
        )}

        {agents && agents.length > 0 && <AgentTable agents={agents} running={running} />}

        {st.errors.length > 0 && (
          <section className="card dash-card">
            <h2 className="dash-title">
              <TriangleAlert size={14} /> Errors
            </h2>
            <Table<ErrorCount>
              size="small"
              pagination={false}
              rowKey="message"
              dataSource={st.errors}
              columns={[
                { title: "Error", dataIndex: "message", className: "mono" },
                { title: "Count", key: "count", align: "right", render: (_, e) => e.count.toLocaleString() },
              ]}
            />
          </section>
        )}
      </div>
    </div>
  );
}

const AGENT_STATE: Record<AgentStatus["state"], { label: string; icon: typeof Check }> = {
  running: { label: "Running", icon: Play },
  done: { label: "Done", icon: Check },
  failed: { label: "Failed", icon: X },
};

/** Each agent's share of a distributed test. */
function AgentTable({ agents, running }: { agents: AgentStatus[]; running: boolean }) {
  const cpu = (a: AgentStatus) => (running ? a.cpu : a.peakCPU);
  const columns: TableColumnsType<AgentStatus> = [
    { title: "Agent", dataIndex: "name", className: "mono agent-name" },
    { title: "Segment", key: "segment", align: "right", render: (_, a) => `${a.segment + 1} of ${agents.length}` },
    { title: running ? "VUs" : "Max VUs", key: "vus", align: "right", render: (_, a) => a.vus.toLocaleString() },
    { title: "Requests", key: "requests", align: "right", render: (_, a) => a.requests.toLocaleString() },
    ...(running ? [{ title: "req/s", key: "rps", align: "right" as const, render: (_: unknown, a: AgentStatus) => (a.rps ?? 0).toFixed(1) }] : []),
    { title: running ? "CPU" : "Peak CPU", key: "cpu", align: "right", render: (_, a) => (cpu(a) >= 0 ? `${Math.round(cpu(a) * 100)}%` : "–") },
    {
      title: "State",
      key: "state",
      render: (_, a) => {
        const s = AGENT_STATE[a.state] ?? AGENT_STATE.done;
        return (
          <span className={`agent-state is-${a.state}`}>
            <s.icon size={13} aria-hidden /> {s.label}
            {a.error && <span className="muted small"> · {a.error}</span>}
          </span>
        );
      },
    },
  ];
  return (
    <section className="card dash-card">
      <h2 className="dash-title">
        <Network size={14} /> Agents
      </h2>
      <Table<AgentStatus> className="agent-table" size="small" pagination={false} scroll={{ x: "max-content" }} rowKey="segment" dataSource={agents} columns={columns} />
    </section>
  );
}

function thresholdActual(t: ThresholdResult) {
  if (t.condition.startsWith("rate")) return t.actual.toFixed(4).replace(/\.?0+$/, "");
  if (/duration|rtt|connecting/.test(t.metric)) return formatMs(t.actual);
  return String(Math.round(t.actual * 100) / 100);
}

/** A stat tile in the style of antd Statistic. Heat shows as a swatch, never as text colour. */
function Kpi({ label, value, unit, sub, heat }: { label: string; value: string; unit?: string; sub?: string; heat?: string }) {
  return (
    <div className="card kpi">
      <span className="kpi-label">{label}</span>
      <span className="kpi-value">
        {heat && <span className={`swatch heat-${heat}`} aria-hidden />}
        {value}
        {unit && <span className="kpi-unit">{unit}</span>}
      </span>
      {sub && <span className="kpi-sub">{sub}</span>}
    </div>
  );
}

/** A duration with its heat swatch beside it (text stays neutral). */
function HeatValue({ ms }: { ms: number }) {
  return (
    <span className="heat-value">
      <span className={`swatch heat-${heatOf(ms)}`} aria-hidden />
      {formatMs(ms)}
    </span>
  );
}

function TickTable({ ticks }: { ticks: LoadTick[] }) {
  return (
    <Table<LoadTick>
      className="tick-table"
      size="small"
      pagination={false}
      scroll={{ x: "max-content", y: 420 }}
      rowKey="elapsed"
      dataSource={ticks}
      columns={[
        { title: "Time", key: "time", className: "mono", render: (_, t) => clock(t.elapsed) },
        { title: "VUs", dataIndex: "vus", align: "right" },
        { title: "req/s", key: "rps", align: "right", render: (_, t) => t.rps.toFixed(1) },
        { title: "Failed", key: "failed", align: "right", render: (_, t) => `${(t.errorRate * 100).toFixed(2)}%` },
        { title: "p50", key: "p50", align: "right", render: (_, t) => formatMs(t.latency.p50) },
        { title: "p95", key: "p95", align: "right", render: (_, t) => formatMs(t.latency.p95) },
        { title: "p99", key: "p99", align: "right", render: (_, t) => formatMs(t.latency.p99) },
      ]}
    />
  );
}
