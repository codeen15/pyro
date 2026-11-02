import { useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Checkbox, InputNumber, Progress, Table, type TableColumnsType } from "antd";
import { Check, FileUp, Minus, Play, Square, X } from "../icons";
import { api, type RunItem, type RunView as Run } from "../api";
import { subscribe } from "../events";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";
import { KindBadge } from "./Sidebar";
import { TestsView } from "./ResponseView";

/** Runs a collection or folder and shows results live. */
export function RunnerView({ path }: { path: string }) {
  const qc = useQueryClient();
  const environment = useApp((s) => s.environment);
  const [iterations, setIterations] = useState(1);
  const [delayMs, setDelayMs] = useState(0);
  const [bail, setBail] = useState(false);
  const [think, setThink] = useState(false);
  const [data, setData] = useState<{ name: string; rows: Record<string, string>[] } | null>(null);
  const [dataError, setDataError] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [open, setOpen] = useState<number | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const runId = run?.id;
  useEffect(() => {
    if (!runId) return;
    // Merge by position: result n is the n-th finished request.
    const merge = (items: RunItem[], from: number, patch: Partial<Run> = {}) =>
      setRun((cur) => {
        if (!cur || cur.id !== runId) return cur;
        const results = cur.results.slice();
        items.forEach((it, i) => (results[from + i] = it));
        return { ...cur, ...patch, results, done: Math.max(cur.done, results.length) };
      });
    const off = subscribe<{ done: number; total: number; item: RunItem } & Run>(`run:${runId}`, (e) => {
      if (e.type === "result") merge([e.data.item], e.data.done - 1);
      if (e.type === "finished") {
        merge(e.data.results, 0, { status: e.data.status, summary: e.data.summary });
        qc.invalidateQueries({ queryKey: ["history"] });
      }
    });
    api.run(runId).then((r) => merge(r.results, 0, { status: r.status, summary: r.summary }), () => {});
    return off;
  }, [runId, qc]);

  const start = async () => {
    setStartError(null);
    setOpen(null);
    try {
      const r = await api.startRun({
        path,
        environment: environment ?? undefined,
        iterations: data ? undefined : iterations,
        data: data?.rows,
        delayMs: delayMs || undefined,
        bail,
        think: think || undefined,
      });
      setRun(r);
    } catch (e) {
      setStartError((e as Error).message);
    }
  };

  const onFile = async (f: File) => {
    setDataError(null);
    try {
      setData({ name: f.name, rows: parseData(f.name, await f.text()) });
    } catch (e) {
      setDataError(`${f.name}: ${(e as Error).message}`);
      setData(null);
    } finally {
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  const running = run?.status === "running";
  const stats = useMemo(() => tally(run?.results ?? []), [run]);
  const rows = useMemo(() => toRows(run?.results ?? []), [run]);
  const pct = !run ? 0 : run.status !== "running" ? 100 : run.total > 0 ? Math.min(100, (run.done / run.total) * 100) : 0;

  return (
    <div className="page runner">
      <div className="dash">
        <section className="card dash-card">
          <header className="runner-head">
            <h1>Run {path}</h1>
            <p className="muted">
              Sends every request in order with its pre- and post-processors. Values captured by one request feed the next.
              {environment ? (
                <>
                  {" "}Environment: <code>{environment}</code>.
                </>
              ) : (
                " No environment selected."
              )}
            </p>
          </header>

          <div className="run-config">
            <label>
              <span className="field-label">Iterations</span>
              <InputNumber
                className="num-input"
                min={1}
                max={10000}
                value={data ? data.rows.length : iterations}
                disabled={!!data || running}
                onChange={(v) => setIterations(Math.max(1, Number(v) || 1))}
              />
            </label>
            <label>
              <span className="field-label">Delay (ms)</span>
              <InputNumber className="num-input" min={0} value={delayMs} disabled={running} onChange={(v) => setDelayMs(Math.max(0, Number(v) || 0))} />
            </label>
            <div>
              <span className="field-label">Data file</span>
              {data ? (
                <span className="data-chip">
                  {data.name} · {data.rows.length} rows
                  <Button type="text" size="small" icon={<X size={12} />} onClick={() => setData(null)} aria-label="Remove data file" disabled={running} />
                </span>
              ) : (
                <Button icon={<FileUp size={13} />} onClick={() => fileRef.current?.click()} disabled={running}>
                  CSV or JSON
                </Button>
              )}
              <input ref={fileRef} type="file" accept=".csv,.json,text/csv,application/json" hidden onChange={(e) => e.target.files?.[0] && onFile(e.target.files[0])} />
            </div>
            <Checkbox className="run-bail" checked={bail} onChange={(e) => setBail(e.target.checked)} disabled={running}>
              Stop at first failure
            </Checkbox>
            <div className="run-think">
              <Checkbox checked={think} onChange={(e) => setThink(e.target.checked)} disabled={running}>
                Apply think time
              </Checkbox>
              <span className="muted small">Wait each request's timer, as a load test would.</span>
            </div>
            <div className="run-actions">
              {running ? (
                <Button icon={<Square size={13} />} onClick={() => api.cancelRun(run!.id)}>
                  Stop
                </Button>
              ) : (
                <Button type="primary" icon={<Play size={14} />} onClick={start}>
                  {run ? "Run again" : "Run"}
                </Button>
              )}
            </div>
          </div>
          {dataError && <Alert className="run-alert" type="error" showIcon title={dataError} />}
          {startError && <Alert className="run-alert" type="error" showIcon title={startError} />}
        </section>

        {run && (
          <section className="card dash-card">
            <Progress
              className="run-progress"
              percent={pct}
              showInfo={false}
              size="small"
              status={running ? "active" : stats.failed > 0 ? "exception" : "normal"}
              aria-valuenow={run.done}
              aria-valuemin={0}
              aria-valuemax={run.total}
              aria-label="Run progress"
            />
            <div className="run-stats">
              <span className={stats.failed > 0 ? "tests-fail" : run.status === "running" ? "" : "tests-pass"}>
                {run.status === "running" ? `Running… ${run.done} of ${run.total}` : stats.failed > 0 ? "Failed" : run.status === "cancelled" ? "Stopped" : "Passed"}
              </span>
              <span className="meta">
                {stats.requests} requests · {stats.failed} failed
              </span>
              <span className="meta">
                {stats.testsPassed + stats.testsFailed} tests · {stats.testsFailed} failed
              </span>
              {run.summary && <span className="meta">{formatMs(run.summary.durationMs)}</span>}
              {run.summary?.stopped && <span className="inline-warn">{run.summary.stopped}</span>}
            </div>
            <Table<Row>
              className="runner-table"
              size="small"
              pagination={false}
              scroll={{ x: "max-content" }}
              rowKey="index"
              dataSource={rows}
              rowClassName={(r) => `runner-row ${r.failed ? "is-fail" : ""}`}
              expandable={{
                expandedRowKeys: open === null ? [] : [open],
                expandRowByClick: true,
                rowExpandable: (r) => !r.item.note && !!r.item.result,
                onExpand: (expanded, r) => setOpen(expanded ? r.index : null),
                expandedRowRender: ({ item: it }) =>
                  it.result && (
                    <div className="run-detail">
                      {it.result.error && <p className="inline-error">{it.result.error}</p>}
                      {it.result.scriptErrors?.map((e, j) => (
                        <p key={j} className="inline-error mono">
                          {e}
                        </p>
                      ))}
                      {it.result.sent && (
                        <p className="mono small muted">
                          {it.result.sent.method ?? "GET"} {it.result.sent.url}
                        </p>
                      )}
                      <TestsView result={it.result} />
                    </div>
                  ),
              }}
              columns={columns}
            />
          </section>
        )}
      </div>
    </div>
  );
}

interface Row {
  index: number;
  item: RunItem;
  failed: boolean;
}

function toRows(results: RunItem[]): Row[] {
  const out: Row[] = [];
  results.forEach((item, index) => {
    if (!item) return;
    const r = item.result;
    const failedTests = (r?.tests ?? []).filter((t) => !t.passed).length;
    const failed = !item.note && (!r || !!r.error || (r.scriptErrors?.length ?? 0) > 0 || failedTests > 0);
    out.push({ index, item, failed });
  });
  return out;
}

const columns: TableColumnsType<Row> = [
  {
    title: <span className="sr-only">Result</span>,
    key: "mark",
    className: "run-mark",
    render: (_, { item, failed }) =>
      item.note ? <Minus size={14} aria-label="skipped" /> : failed ? <X size={14} aria-label="failed" /> : <Check size={14} aria-label="passed" />,
  },
  { title: "#", key: "iteration", className: "muted", render: (_, { item }) => item.iteration + 1 },
  {
    title: "Request",
    key: "request",
    render: (_, { item }) => (
      <>
        <span className="run-req">
          <KindBadge kind={item.kind} method={item.result?.sent?.method} />
          <span>{item.name}</span>
          <span className="muted mono small">{item.path}</span>
        </span>
        {item.note && <span className="muted small"> {item.note}</span>}
      </>
    ),
  },
  {
    title: "Status",
    key: "status",
    render: (_, { item: { result: r } }) =>
      r?.response ? r.response.status : r?.skipped ? <span className="muted">skipped</span> : r?.error ? <span className="hist-err">error</span> : "",
  },
  {
    title: "Time",
    key: "time",
    align: "right",
    className: "mono",
    render: (_, { item }) => {
      const ms = item.result?.response?.timings.total;
      return (
        ms !== undefined && (
          <span className="heat-value">
            <span className={`swatch heat-${heatOf(ms)}`} aria-hidden />
            {formatMs(ms)}
          </span>
        )
      );
    },
  },
  {
    title: "Tests",
    key: "tests",
    align: "right",
    render: (_, { item }) => {
      const tests = item.result?.tests ?? [];
      const failedTests = tests.filter((t) => !t.passed).length;
      return tests.length > 0 ? (
        <span className={failedTests ? "tests-fail" : "tests-pass"}>
          {tests.length - failedTests}/{tests.length}
        </span>
      ) : (
        ""
      );
    },
  },
];

function tally(items: RunItem[]) {
  let requests = 0,
    failed = 0,
    testsPassed = 0,
    testsFailed = 0;
  for (const it of items) {
    if (!it || it.note || !it.result) continue;
    requests++;
    const r = it.result;
    const tf = (r.tests ?? []).filter((t) => !t.passed).length;
    testsFailed += tf;
    testsPassed += (r.tests?.length ?? 0) - tf;
    if (r.error || (r.scriptErrors?.length ?? 0) > 0 || tf > 0) failed++;
  }
  return { requests, failed, testsPassed, testsFailed };
}

/** Parses a CSV (header row) or JSON array data file into rows. */
export function parseData(name: string, text: string): Record<string, string>[] {
  const t = text.replace(/^﻿/, "").trim();
  if (name.toLowerCase().endsWith(".json") || t.startsWith("[")) {
    const arr = JSON.parse(t);
    if (!Array.isArray(arr)) throw new Error("JSON data must be an array of objects");
    return arr.map((o: Record<string, unknown>) =>
      Object.fromEntries(Object.entries(o).map(([k, v]) => [k, typeof v === "string" ? v : v === null ? "" : JSON.stringify(v)])),
    );
  }
  const rows = parseCSV(t);
  if (rows.length < 2) throw new Error("needs a header row and at least one data row");
  const head = rows[0].map((h) => h.trim());
  return rows.slice(1).map((r) => Object.fromEntries(head.map((h, i) => [h, r[i] ?? ""])));
}

function parseCSV(s: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = "";
  let quoted = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quoted) {
      if (c === '"' && s[i + 1] === '"') {
        field += '"';
        i++;
      } else if (c === '"') quoted = false;
      else field += c;
    } else if (c === '"') quoted = true;
    else if (c === ",") {
      row.push(field);
      field = "";
    } else if (c === "\n" || c === "\r") {
      if (c === "\r" && s[i + 1] === "\n") i++;
      row.push(field);
      rows.push(row);
      row = [];
      field = "";
    } else field += c;
  }
  row.push(field);
  rows.push(row);
  return rows.filter((r) => r.some((f) => f !== ""));
}
