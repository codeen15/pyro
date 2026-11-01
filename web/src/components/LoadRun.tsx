import { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Check, Download, ExternalLink, Minus, Network, Play, Square, Table2, TriangleAlert, X } from "lucide-react";
import { api, type AgentStatus, type ErrorCount, type LiveSnapshot, type LoadRunView, type LoadTick, type RequestStat, type ThresholdResult } from "../api";
import { subscribe } from "../events";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";
import { LineChart, clock, fmtValue, type Ref } from "./LineChart";

const VERDICT: Record<string, { label: string; icon: typeof Check }> = {
  passed: { label: "Passed", icon: Check },
  failed: { label: "Failed", icon: X },
  aborted: { label: "Aborted", icon: X },
  stopped: { label: "Stopped", icon: Square },
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
  agents?: AgentStatus[];
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

  if (error) return <p className="pad inline-error">{error}</p>;
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
    <section className="loadrun">
      <header className="lt-head">
        <div>
          <h1>
            {st.name || st.test || "Load test"}
            {running ? (
              <span className="verdict v-running">Running · {clock(last?.elapsed ?? 0)}</span>
            ) : (
              v && (
                <span className={`verdict v-${st.status}`}>
                  <v.icon size={14} /> {v.label}
                </span>
              )
            )}
          </h1>
          <p className="muted small">
            {st.test && (
              <button className="link" onClick={() => select(`load:${st.test}`)}>
                loadtests/{st.test}.load.yaml
              </button>
            )}
            {st.reason && <> · {st.reason}</>}
          </p>
        </div>
        <div className="lt-actions">
          {running ? (
            <>
              <button
                className="btn ghost"
                disabled={stopping}
                onClick={() => {
                  setStopping(true);
                  api.stopLoadRun(id);
                }}
                title="No new iterations; in-flight ones finish"
              >
                <Square size={13} /> {stopping ? "Stopping…" : "Stop"}
              </button>
              {stopping && (
                <button className="btn ghost" onClick={() => api.stopLoadRun(id, true)} title="Cancel in-flight requests now">
                  Abort now
                </button>
              )}
            </>
          ) : (
            <>
              <button className="btn ghost" onClick={() => openReport(false)}>
                <ExternalLink size={13} /> Report
              </button>
              <button className="btn ghost" onClick={() => openReport(true)} aria-label="Download report">
                <Download size={13} />
              </button>
            </>
          )}
        </div>
      </header>

      <div className="kpis">
        <Kpi label="Virtual users" value={running ? String(last?.vus ?? 0) : `${st.maxVUs ?? 0} max`} />
        {hasHTTP && <Kpi label="Requests per second" value={running ? fmtValue(last?.rps ?? 0, "") : fmtValue(st.totals?.http_reqs?.perSecond ?? 0, "")} />}
        {hasHTTP && (
          <div className="kpi">
            <span className="kpi-label">p95 response time{running ? " (last second)" : ""}</span>
            <span className={`heat-chip big heat-${heatOf(p95)}`}>{formatMs(p95)}</span>
          </div>
        )}
        {hasHTTP && <Kpi label="Requests" value={totalReqs.toLocaleString()} sub={`${totalFailed.toLocaleString()} failed (${totalReqs ? ((totalFailed / totalReqs) * 100).toFixed(2) : "0.00"}%)`} />}
        {charts.ws && <Kpi label="Stream messages per second" value={fmtValue(last?.wsMessagesPerSecond ?? 0, "")} />}
        {(last?.dropped ?? 0) > 0 && <Kpi label="Dropped iterations (last second)" value={String(last?.dropped)} sub="Not enough VUs for the arrival rate" />}
      </div>

      {st.thresholds.length > 0 && (
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
                  <button className="btn ghost small" onClick={() => setSeen(new Set(seen).add(key))}>
                    Seen
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <div className="chart-head">
        <h2 className="sub-head">Over time</h2>
        <button className="btn ghost small" onClick={() => setShowTable(!showTable)} aria-pressed={showTable}>
          <Table2 size={13} /> {showTable ? "Charts" : "Table view"}
        </button>
      </div>
      {showTable ? (
        <TickTable ticks={st.ticks} />
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
        <>
          <h2 className="sub-head">Requests{running ? " (so far)" : ""}</h2>
          <div className="scroll-x">
            <table className="data-table req-table">
              <thead>
                <tr>
                  <th>Request</th>
                  <th>Scenario</th>
                  <th className="num">Count</th>
                  <th className="num">Failed</th>
                  <th className="num">req/s</th>
                  <th className="num">Avg</th>
                  <th className="num">p50</th>
                  <th className="num">p95</th>
                  <th className="num">p99</th>
                  <th className="num">Max</th>
                </tr>
              </thead>
              <tbody>
                {st.requests.map((r) => (
                  <tr key={r.scenario + r.request}>
                    <td className="mono">
                      {r.method} {r.request}
                    </td>
                    <td>{r.scenario}</td>
                    <td className="num">{r.count.toLocaleString()}</td>
                    <td className={`num ${r.failed ? "fail-text" : ""}`}>{r.failed.toLocaleString()}</td>
                    <td className="num">{r.perSecond.toFixed(1)}</td>
                    <td className="num">{formatMs(r.duration.avg ?? 0)}</td>
                    <td className="num">{formatMs(r.duration.med ?? 0)}</td>
                    <HeatCell ms={r.duration.p95 ?? 0} />
                    <HeatCell ms={r.duration.p99 ?? 0} />
                    <td className="num">{formatMs(r.duration.max ?? 0)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {agents && agents.length > 0 && <AgentTable agents={agents} running={running} />}

      {st.errors.length > 0 && (
        <>
          <h2 className="sub-head">
            <TriangleAlert size={13} /> Errors
          </h2>
          <table className="data-table">
            <tbody>
              {st.errors.map((e) => (
                <tr key={e.message}>
                  <td className="mono">{e.message}</td>
                  <td className="num">{e.count.toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}

const AGENT_STATE: Record<AgentStatus["state"], { label: string; icon: typeof Check }> = {
  running: { label: "Running", icon: Play },
  done: { label: "Done", icon: Check },
  failed: { label: "Failed", icon: X },
};

/** Each agent's share of a distributed test. */
function AgentTable({ agents, running }: { agents: AgentStatus[]; running: boolean }) {
  return (
    <>
      <h2 className="sub-head">
        <Network size={13} /> Agents
      </h2>
      <div className="scroll-x">
        <table className="data-table agent-table">
          <thead>
            <tr>
              <th>Agent</th>
              <th className="num">Segment</th>
              <th className="num">{running ? "VUs" : "Max VUs"}</th>
              <th className="num">Requests</th>
              {running && <th className="num">req/s</th>}
              <th>State</th>
            </tr>
          </thead>
          <tbody>
            {agents.map((a) => {
              const s = AGENT_STATE[a.state] ?? AGENT_STATE.done;
              return (
                <tr key={a.segment}>
                  <td className="mono">{a.name}</td>
                  <td className="num">
                    {a.segment + 1} of {agents.length}
                  </td>
                  <td className="num">{a.vus.toLocaleString()}</td>
                  <td className="num">{a.requests.toLocaleString()}</td>
                  {running && <td className="num">{(a.rps ?? 0).toFixed(1)}</td>}
                  <td className={`agent-state is-${a.state}`}>
                    <s.icon size={13} aria-hidden /> {s.label}
                    {a.error && <span className="muted small"> · {a.error}</span>}
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

function thresholdActual(t: ThresholdResult) {
  if (t.condition.startsWith("rate")) return t.actual.toFixed(4).replace(/\.?0+$/, "");
  if (/duration|rtt|connecting/.test(t.metric)) return formatMs(t.actual);
  return String(Math.round(t.actual * 100) / 100);
}

function Kpi({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="kpi">
      <span className="kpi-label">{label}</span>
      <span className="kpi-value">{value}</span>
      {sub && <span className="kpi-sub">{sub}</span>}
    </div>
  );
}

/** A duration with its heat swatch beside it (text stays neutral). */
function HeatCell({ ms }: { ms: number }) {
  return (
    <td className="num">
      <span className={`swatch heat-${heatOf(ms)}`} aria-hidden />
      {formatMs(ms)}
    </td>
  );
}

function TickTable({ ticks }: { ticks: LoadTick[] }) {
  return (
    <div className="scroll-x tick-table">
      <table className="data-table">
        <thead>
          <tr>
            <th>Time</th>
            <th className="num">VUs</th>
            <th className="num">req/s</th>
            <th className="num">Failed</th>
            <th className="num">p50</th>
            <th className="num">p95</th>
            <th className="num">p99</th>
          </tr>
        </thead>
        <tbody>
          {ticks.map((t) => (
            <tr key={t.elapsed}>
              <td className="mono">{clock(t.elapsed)}</td>
              <td className="num">{t.vus}</td>
              <td className="num">{t.rps.toFixed(1)}</td>
              <td className="num">{(t.errorRate * 100).toFixed(2)}%</td>
              <td className="num">{formatMs(t.latency.p50)}</td>
              <td className="num">{formatMs(t.latency.p95)}</td>
              <td className="num">{formatMs(t.latency.p99)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
