import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, FileInput, TriangleAlert, X } from "lucide-react";
import { api, ApiError, type ImportReport } from "./api";
import { useApp } from "./store";
import { Sidebar, KindBadge } from "./components/Sidebar";
import { RequestEditor } from "./components/RequestEditor";
import { ResponseView, HeatLegend } from "./components/ResponseView";
import { SessionConsole } from "./components/SessionConsole";
import { RunnerView } from "./components/RunnerView";
import { LoadTestPage, NewLoadTest } from "./components/LoadTest";
import { LoadRunPage } from "./components/LoadRun";
import { CompareRunsPage } from "./components/CompareRuns";
import { InstallPlugin, PluginPage } from "./components/Plugins";

export function App() {
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, refetchInterval: 10_000 });
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const selected = useApp((s) => s.selected);
  const [report, setReport] = useState<ImportReport | null>(null);

  const unauthorized = ws.error instanceof ApiError && ws.error.status === 401;

  return (
    <div className="shell">
      <TopBar workspace={ws.data?.name} onImported={setReport} />
      <Sidebar ws={ws.data} />
      <main className="main">
        {unauthorized ? (
          <Blocker title="This tab isn't connected to Pyro">
            Open the link that <code>pyro serve</code> printed in your terminal. It carries a one-time session token.
          </Blocker>
        ) : ws.error ? (
          <Blocker title="Couldn't load the workspace">{ws.error.message}</Blocker>
        ) : report ? (
          <ImportResult report={report} onClose={() => setReport(null)} />
        ) : selected === "plugins:install" ? (
          <InstallPlugin />
        ) : selected?.startsWith("plugin:") ? (
          <PluginPage key={selected} name={selected.slice(7)} />
        ) : selected === "load:new" ? (
          <NewLoadTest />
        ) : selected?.startsWith("load:") ? (
          <LoadTestPage key={selected} name={selected.slice(5)} />
        ) : selected?.startsWith("compare:") ? (
          <CompareRunsPage key={selected} a={selected.split(":")[1]} b={selected.split(":")[2]} />
        ) : selected?.startsWith("loadrun:") ? (
          <LoadRunPage key={selected} id={selected.slice(8)} />
        ) : selected?.startsWith("run:") ? (
          <RunnerView key={selected} path={selected.slice(4)} />
        ) : selected?.startsWith("history:") ? (
          <HistoryReplay id={selected} />
        ) : selected ? (
          <Workspace path={selected} />
        ) : (
          <Welcome hasCollections={(ws.data?.collections.length ?? 0) > 0} />
        )}
      </main>
      <footer className="statusbar">
        <span className={`dot ${health.isSuccess ? "ok" : health.isError ? "bad" : ""}`} aria-hidden />
        {health.isSuccess ? `Engine ${health.data.version}` : health.isError ? "Engine unreachable. Is pyro serve still running?" : "Connecting…"}
        {ws.data && <span className="path-hint mono">{ws.data.root}</span>}
      </footer>
    </div>
  );
}

function TopBar({ workspace, onImported }: { workspace?: string; onImported: (r: ImportReport) => void }) {
  const qc = useQueryClient();
  const envs = useQuery({ queryKey: ["environments"], queryFn: api.environments });
  const environment = useApp((s) => s.environment);
  const setEnvironment = useApp((s) => s.setEnvironment);
  const fileRef = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);

  // A remembered environment may have been deleted on disk since.
  const envValue = environment && envs.data?.includes(environment) ? environment : "";

  const onFile = async (file: File) => {
    setImporting(true);
    setImportError(null);
    try {
      const report = await api.importFile(await file.text());
      await Promise.all([qc.invalidateQueries({ queryKey: ["workspace"] }), qc.invalidateQueries({ queryKey: ["environments"] })]);
      onImported(report);
    } catch (e) {
      setImportError(`${file.name}: ${(e as Error).message}`);
    } finally {
      setImporting(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  return (
    <header className="topbar">
      <span className="brand">Pyro</span>
      {workspace && <span className="ws-name">{workspace}</span>}
      <div className="topbar-right">
        {importError && (
          <span className="topbar-error" role="alert">
            <TriangleAlert size={14} /> {importError}
            <button className="icon-btn" onClick={() => setImportError(null)} aria-label="Dismiss">
              <X size={13} />
            </button>
          </span>
        )}
        <label className="env-select">
          <span>Environment</span>
          <select value={envValue} onChange={(e) => setEnvironment(e.target.value || null)}>
            <option value="">No environment</option>
            {envs.data?.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <button className="btn shell-btn" onClick={() => fileRef.current?.click()} disabled={importing}>
          <FileInput size={14} />
          {importing ? "Importing…" : "Import"}
        </button>
        <input
          ref={fileRef}
          type="file"
          accept=".json,application/json"
          hidden
          onChange={(e) => e.target.files?.[0] && onFile(e.target.files[0])}
        />
      </div>
    </header>
  );
}

function Workspace({ path }: { path: string }) {
  const q = useQuery({ queryKey: ["request", path], queryFn: () => api.request(path), retry: false });
  const result = useApp((s) => s.results[path]);
  const draft = useApp((s) => s.drafts[path]);
  const sending = useApp((s) => !!s.sending[path]);
  const select = useApp((s) => s.select);
  const split = useSplit();

  if (q.isPending) return <div className="pad"><span className="skeleton line" style={{ width: "40%" }} /></div>;
  if (q.error) {
    const gone = q.error instanceof ApiError && q.error.status === 404;
    return (
      <Blocker title={gone ? "This request no longer exists" : "Couldn't open the request"}>
        {gone ? "It may have been renamed or deleted on disk." : q.error.message}{" "}
        <button className="btn ghost small" onClick={() => select(null)}>
          Close
        </button>
      </Blocker>
    );
  }
  // Route by the draft's type so switching type in the editor takes effect.
  const kind = draft?.kind ?? q.data.kind;
  if (kind === "websocket" || kind === "socketio") {
    return <SessionConsole key={path} path={path} saved={q.data} />;
  }
  return (
    <div className="work" style={{ gridTemplateRows: `${split.top}fr 6px ${100 - split.top}fr` }} ref={split.ref}>
      <RequestEditor key={path} path={path} saved={q.data} />
      <div className="splitter" role="separator" aria-orientation="horizontal" aria-label="Resize response panel" tabIndex={0} {...split.handlers} />
      <ResponseView key={`r-${path}`} result={result} sending={sending} />
    </div>
  );
}

/** A draggable horizontal split between the editor and the response (percent for the top pane). */
function useSplit() {
  const [top, setTop] = useState(() => {
    try {
      return Number(localStorage.getItem("pyro.split")) || 45;
    } catch {
      return 45;
    }
  });
  const ref = useRef<HTMLDivElement>(null);
  const commit = (v: number) => {
    const clamped = Math.min(80, Math.max(20, v));
    setTop(clamped);
    try {
      localStorage.setItem("pyro.split", String(clamped));
    } catch {
      /* storage unavailable */
    }
  };
  return {
    top,
    ref,
    handlers: {
      onPointerDown: (e: React.PointerEvent) => {
        const el = ref.current;
        if (!el) return;
        e.currentTarget.setPointerCapture(e.pointerId);
        const rect = el.getBoundingClientRect();
        const move = (ev: PointerEvent) => commit(((ev.clientY - rect.top) / rect.height) * 100);
        const up = () => {
          window.removeEventListener("pointermove", move);
          window.removeEventListener("pointerup", up);
        };
        window.addEventListener("pointermove", move);
        window.addEventListener("pointerup", up);
      },
      onKeyDown: (e: React.KeyboardEvent) => {
        if (e.key === "ArrowUp") commit(top - 5);
        if (e.key === "ArrowDown") commit(top + 5);
      },
    },
  };
}

function HistoryReplay({ id }: { id: string }) {
  const result = useApp((s) => s.results[id]);
  const sent = result?.sent;
  return (
    <div className="work replay">
      <section className="editor">
        <header className="req-head">
          <div className="req-head-text">
            <h1 className="req-name static">Sent request</h1>
            <span className="req-path mono">Not saved in a collection{result && ` · ${new Date(result.startedAt).toLocaleString()}`}</span>
          </div>
        </header>
        {sent && (
          <div className="urlbar static">
            <KindBadge method={sent.method} />
            <code className="mono url-static">{sent.url}</code>
          </div>
        )}
      </section>
      <ResponseView result={result} sending={false} />
    </div>
  );
}

function ImportResult({ report, onClose }: { report: ImportReport; onClose: () => void }) {
  const select = useApp((s) => s.select);
  const setTab = useApp((s) => s.setSidebarTab);
  return (
    <section className="report" aria-label="Import result">
      <header className="report-head">
        <CircleCheck size={20} className="ok-icon" />
        <div>
          <h1>Imported {report.collection ? `“${report.collection}”` : "environment"}</h1>
          <p className="muted">
            {report.format}
            {report.slug && (
              <>
                {" "}· {report.requests} requests in {report.folders} folders, saved to <code>collections/{report.slug}</code>
              </>
            )}
            {report.environments?.map((e) => (
              <span key={e}>
                {" "}· environment <code>{e}</code>
              </span>
            ))}
          </p>
        </div>
        <button className="icon-btn" onClick={onClose} aria-label="Close import result">
          <X size={16} />
        </button>
      </header>

      {report.warnings.length === 0 ? (
        <p>Everything converted. Nothing needs your attention.</p>
      ) : (
        <>
          <h2>
            {report.warnings.length} thing{report.warnings.length > 1 ? "s" : ""} to check
          </h2>
          <table className="data-table">
            <thead>
              <tr>
                <th>Where</th>
                <th>What happened</th>
              </tr>
            </thead>
            <tbody>
              {report.warnings.map((w, i) => (
                <tr key={i}>
                  <td className="mono key">
                    {w.path && report.slug ? (
                      <button
                        className="link"
                        onClick={() => {
                          setTab("collections");
                          select(`${report.slug}/${w.path}`);
                          onClose();
                        }}
                      >
                        {w.path}
                      </button>
                    ) : (
                      "Whole collection"
                    )}
                  </td>
                  <td>{w.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      <div className="report-actions">
        <button className="btn primary" onClick={onClose}>
          Done
        </button>
      </div>
    </section>
  );
}

function Welcome({ hasCollections }: { hasCollections: boolean }) {
  return (
    <section className="welcome">
      <h1>{hasCollections ? "Pick a request to start" : "Bring your API in"}</h1>
      <p className="muted">
        {hasCollections
          ? "Choose a request in the sidebar, or press + on a collection to add one. Send with Ctrl + Enter, save with Ctrl + S."
          : "Import a Postman collection or environment with Import in the top bar. Pyro writes it as YAML files you can commit."}
      </p>
      <div className="welcome-legend">
        <h2>Reading response times</h2>
        <p className="muted">Times glow hotter as they get slower, from cold steel to white heat.</p>
        <HeatLegend />
      </div>
    </section>
  );
}

function Blocker({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="blocker" role="alert">
      <TriangleAlert size={20} />
      <h1>{title}</h1>
      <p>{children}</p>
    </section>
  );
}
