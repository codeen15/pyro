import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CircleCheck, Cookie, FileInput, SlidersHorizontal, TriangleAlert, X } from "lucide-react";
import { api, ApiError, decodeBody, type ImportReport, type PyroResponse } from "./api";
import { applyTheme, useApp, type Theme } from "./store";
import { Sidebar, KindBadge } from "./components/Sidebar";
import { RequestEditor } from "./components/RequestEditor";
import { ResponseView, HeatLegend } from "./components/ResponseView";
import { SessionConsole } from "./components/SessionConsole";
import { RunnerView } from "./components/RunnerView";
import { LoadTestPage, NewLoadTest } from "./components/LoadTest";
import { LoadRunPage } from "./components/LoadRun";
import { CompareRunsPage } from "./components/CompareRuns";
import { InstallPlugin, PluginPage } from "./components/Plugins";
import { ImportPage } from "./components/ImportPage";
import { VariablesPage } from "./components/Variables";
import { CookiesPage } from "./components/Cookies";
import { SettingsPage } from "./components/Settings";

export function App() {
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, refetchInterval: 10_000 });
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const selected = useApp((s) => s.selected);
  const report = useApp((s) => s.report);
  const setReport = useApp((s) => s.setReport);

  const unauthorized = ws.error instanceof ApiError && ws.error.status === 401;

  return (
    <div className="shell">
      <TopBar workspace={ws.data?.name} />
      <Sidebar ws={ws.data} />
      <main className="main">
        {unauthorized ? (
          <Blocker title="This tab isn't connected to Pyro">
            Open the link that <code>pyro serve</code> printed in your terminal. It carries a one-time session token.
          </Blocker>
        ) : ws.error ? (
          <Blocker title="Couldn't load the workspace">{ws.error.message}</Blocker>
        ) : report ? (
          <ImportResult report={report.data} mode={report.mode} onClose={() => setReport(null)} />
        ) : selected?.startsWith("vars:") ? (
          <VariablesPage target={selected.slice(5)} />
        ) : selected?.startsWith("settings:") ? (
          <SettingsPage key={selected} path={selected.slice(9)} />
        ) : selected === "cookies" ? (
          <CookiesPage />
        ) : selected === "import" ? (
          <ImportPage />
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
        <ThemeSelect />
      </footer>
    </div>
  );
}

function TopBar({ workspace }: { workspace?: string }) {
  const envs = useQuery({ queryKey: ["environments"], queryFn: api.environments });
  const environment = useApp((s) => s.environment);
  const setEnvironment = useApp((s) => s.setEnvironment);
  const select = useApp((s) => s.select);
  const setReport = useApp((s) => s.setReport);

  // A remembered environment may have been deleted on disk since.
  const envValue = environment && envs.data?.includes(environment) ? environment : "";

  return (
    <header className="topbar">
      <span className="brand">Pyro</span>
      {workspace && <span className="ws-name">{workspace}</span>}
      <div className="topbar-right">
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
        <button
          className="btn shell-btn icon-only"
          onClick={() => {
            setReport(null);
            select(envValue ? `vars:env:${envValue}` : "vars:globals");
          }}
          title="Environments and globals"
          aria-label="Edit environments and globals"
        >
          <SlidersHorizontal size={14} />
        </button>
        <button
          className="btn shell-btn icon-only"
          onClick={() => {
            setReport(null);
            select("cookies");
          }}
          title="Cookies"
          aria-label="Manage cookies"
        >
          <Cookie size={14} />
        </button>
        <button
          className="btn shell-btn"
          onClick={() => {
            setReport(null);
            select("import");
          }}
        >
          <FileInput size={14} />
          Import
        </button>
      </div>
    </header>
  );
}

const EXAMPLE_SKIP_HEADERS = new Set(["date", "connection", "keep-alive", "content-length", "transfer-encoding", "set-cookie", "age", "expires"]);

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
  const setDraft = useApp.getState().setDraft;
  const saveExample = (res: PyroResponse) => {
    let body = "";
    try {
      body = new TextDecoder("utf-8", { fatal: true }).decode(decodeBody(res.body));
    } catch {
      alert("This response isn't text, so it can't be saved as an example.");
      return;
    }
    // Examples live in git: leave out per-response noise and session cookies,
    // and pretty-print JSON so diffs stay readable.
    const headers = (res.headers ?? []).filter((h) => !EXAMPLE_SKIP_HEADERS.has(h.key.toLowerCase()));
    if (headers.some((h) => h.key.toLowerCase() === "content-type" && /json/i.test(h.value))) {
      try {
        body = JSON.stringify(JSON.parse(body), null, 2);
      } catch {
        /* keep as sent */
      }
    }
    const current = draft ?? q.data;
    const examples = [...(current.examples ?? []), { name: `${res.status} ${res.statusText}`.trim(), status: res.status || undefined, headers, body }];
    setDraft(path, { ...current, examples });
  };
  // Route by the draft's type so switching type in the editor takes effect.
  const kind = draft?.kind ?? q.data.kind;
  if (kind === "websocket" || kind === "socketio") {
    return <SessionConsole key={path} path={path} saved={q.data} />;
  }
  return (
    <div className="work" style={{ gridTemplateRows: `${split.top}fr 6px ${100 - split.top}fr` }} ref={split.ref}>
      <RequestEditor key={path} path={path} saved={q.data} />
      <div className="splitter" role="separator" aria-orientation="horizontal" aria-label="Resize response panel" tabIndex={0} {...split.handlers} />
      <ResponseView key={`r-${path}`} result={result} sending={sending} onSaveExample={result?.response ? () => saveExample(result.response!) : undefined} />
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

function ImportResult({ report, mode, onClose }: { report: ImportReport; mode: "import" | "export"; onClose: () => void }) {
  const select = useApp((s) => s.select);
  const setTab = useApp((s) => s.setSidebarTab);
  return (
    <section className="report" aria-label="Import result">
      <header className="report-head">
        <CircleCheck size={20} className="ok-icon" />
        <div>
          <h1>
            {mode === "export"
              ? `Exported “${report.collection}” to Postman`
              : report.into
                ? `Added ${report.requests} request${report.requests === 1 ? "" : "s"} to ${report.slug}`
                : `Imported ${report.collection ? `“${report.collection}”` : "environment"}`}
          </h1>
          <p className="muted">
            {report.format}
            {report.slug && mode === "export" && (
              <>
                {" "}· {plural(report.requests, "request")} in {plural(report.folders, "folder")}, downloaded as <code>{report.slug}.postman_collection.json</code>
              </>
            )}
            {report.slug && mode === "import" && (
              <>
                {" "}· {report.into ? "" : `${plural(report.requests, "request")} in ${plural(report.folders, "folder")}, `}saved to <code>collections/{report.slug}</code>
              </>
            )}
            {report.environments?.map((e) => (
              <span key={e}>
                {" "}· environment <code>{e}</code>
              </span>
            ))}
            {report.loadTests?.map((l) => (
              <span key={l}>
                {" "}· load test{" "}
                <button className="link" onClick={() => { onClose(); select(`load:${l}`); }}>
                  {l}
                </button>
              </span>
            ))}
          </p>
        </div>
        <button className="icon-btn" onClick={onClose} aria-label="Close import result">
          <X size={16} />
        </button>
      </header>

      {report.warnings.length === 0 ? (
        <p>{mode === "export" ? "Everything converted." : "Everything converted. Nothing needs your attention."}</p>
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

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

function ThemeSelect() {
  const theme = useApp((s) => s.theme);
  const setTheme = useApp((s) => s.setTheme);
  // Follow the system setting live when the preference is "system".
  useEffect(() => {
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    const on = () => applyTheme("system");
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [theme]);
  return (
    <label className="theme-select">
      <span className="sr-only">Theme</span>
      <select value={theme} onChange={(e) => setTheme(e.target.value as Theme)} aria-label="Theme">
        <option value="system">System theme</option>
        <option value="dark">Dark</option>
        <option value="light">Light</option>
      </select>
    </label>
  );
}
