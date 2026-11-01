import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Check, ChevronRight, FileUp, Minus, Play, Square, X } from "lucide-react";
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
  const pct = !run ? 0 : run.status !== "running" ? 100 : run.total > 0 ? Math.min(100, (run.done / run.total) * 100) : 0;

  return (
    <section className="runner">
      <header className="runner-head">
        <div>
          <h1>Run {path}</h1>
          <p className="muted small">
            Sends every request in order with scripts, assertions and extractors. Values captured by one request feed the next.
            {environment ? (
              <>
                {" "}Environment: <code>{environment}</code>.
              </>
            ) : (
              " No environment selected."
            )}
          </p>
        </div>
      </header>

      <div className="run-config">
        <label>
          <span className="field-label">Iterations</span>
          <input type="number" min={1} max={10000} className="cell num-input" value={data ? data.rows.length : iterations} disabled={!!data || running} onChange={(e) => setIterations(Math.max(1, Number(e.target.value) || 1))} />
        </label>
        <label>
          <span className="field-label">Delay (ms)</span>
          <input type="number" min={0} className="cell num-input" value={delayMs} disabled={running} onChange={(e) => setDelayMs(Math.max(0, Number(e.target.value) || 0))} />
        </label>
        <div>
          <span className="field-label">Data file</span>
          {data ? (
            <span className="data-chip">
              {data.name} · {data.rows.length} rows
              <button className="icon-btn" onClick={() => setData(null)} aria-label="Remove data file" disabled={running}>
                <X size={12} />
              </button>
            </span>
          ) : (
            <button className="btn ghost small" onClick={() => fileRef.current?.click()} disabled={running}>
              <FileUp size={13} /> CSV or JSON
            </button>
          )}
          <input ref={fileRef} type="file" accept=".csv,.json,text/csv,application/json" hidden onChange={(e) => e.target.files?.[0] && onFile(e.target.files[0])} />
        </div>
        <label className="check">
          <input type="checkbox" checked={bail} onChange={(e) => setBail(e.target.checked)} disabled={running} />
          Stop at first failure
        </label>
        <div className="run-actions">
          {running ? (
            <button className="btn ghost" onClick={() => api.cancelRun(run!.id)}>
              <Square size={13} /> Stop
            </button>
          ) : (
            <button className="btn primary" onClick={start}>
              <Play size={14} /> {run ? "Run again" : "Run"}
            </button>
          )}
        </div>
      </div>
      {dataError && <p className="inline-error">{dataError}</p>}
      {startError && <p className="inline-error">{startError}</p>}

      {run && (
        <>
          <div className="run-progress" role="progressbar" aria-valuenow={run.done} aria-valuemin={0} aria-valuemax={run.total} aria-label="Run progress">
            <span style={{ width: `${pct}%` }} />
          </div>
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
          <table className="data-table run-table">
            <thead>
              <tr>
                <th aria-label="Result" />
                <th>#</th>
                <th>Request</th>
                <th>Status</th>
                <th className="num">Time</th>
                <th className="num">Tests</th>
              </tr>
            </thead>
            <tbody>
              {run.results.map((it, i) =>
                it ? (
                  <Fragment key={i}>
                    <ResultRow item={it} open={open === i} onToggle={() => setOpen(open === i ? null : i)} />
                    {open === i && it.result && (
                      <tr className="run-detail">
                        <td colSpan={6}>
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
                        </td>
                      </tr>
                    )}
                  </Fragment>
                ) : null,
              )}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}

function ResultRow({ item, open, onToggle }: { item: RunItem; open: boolean; onToggle: () => void }) {
  const r = item.result;
  const tests = r?.tests ?? [];
  const failedTests = tests.filter((t) => !t.passed).length;
  const failed = !item.note && (!r || !!r.error || (r.scriptErrors?.length ?? 0) > 0 || failedTests > 0);
  const ms = r?.response?.timings.total;
  return (
    <tr className={`run-row ${failed ? "is-fail" : ""}`} onClick={item.note ? undefined : onToggle} aria-expanded={item.note ? undefined : open}>
      <td className="mark">
        {item.note ? <Minus size={14} aria-label="skipped" /> : failed ? <X size={14} aria-label="failed" /> : <Check size={14} aria-label="passed" />}
      </td>
      <td className="muted">{item.iteration + 1}</td>
      <td>
        <span className="run-req">
          {!item.note && <ChevronRight size={12} className={`chev ${open ? "open" : ""}`} />}
          <KindBadge kind={item.kind} method={r?.sent?.method} />
          <span>{item.name}</span>
          <span className="muted mono small">{item.path}</span>
        </span>
        {item.note && <span className="muted small"> {item.note}</span>}
      </td>
      <td>{r?.response ? r.response.status : r?.skipped ? <span className="muted">skipped</span> : r?.error ? <span className="hist-err">error</span> : ""}</td>
      <td className={`num mono ${ms !== undefined ? `heat-text-${heatOf(ms)}` : ""}`}>{ms !== undefined ? formatMs(ms) : ""}</td>
      <td className="num">{tests.length > 0 ? <span className={failedTests ? "tests-fail" : "tests-pass"}>{tests.length - failedTests}/{tests.length}</span> : ""}</td>
    </tr>
  );
}

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
