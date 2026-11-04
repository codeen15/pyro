import { useRef, useState } from "react";
import { Alert, App as AntApp, Button, Dropdown, Result, Select, Skeleton, Table, Tag, Tooltip } from "antd";
import { useQuery } from "@tanstack/react-query";
import { BookOpen, Briefcase, ChevronDown, CircleCheck, Cookie, FileInput, SlidersHorizontal, X } from "./icons";
import logo from "./assets/pyro-logo.png";
import { api, ApiError, decodeBody, type ImportReport, type PyroResponse } from "./api";
import { useApp, type Theme } from "./store";
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
import { DocsPage } from "./components/DocsPage";
import { AccountPage, ProjectSwitcher, ProjectsPage, UserMenu } from "./components/Team";
import { useTeam } from "./team";

export function App() {
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, refetchInterval: 10_000 });
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const selected = useApp((s) => s.selected);
  const report = useApp((s) => s.report);
  const setReport = useApp((s) => s.setReport);

  const unauthorized = ws.error instanceof ApiError && ws.error.status === 401;
  const teamMode = useTeam((s) => s.me !== null);

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
        ) : selected === "docs" || selected?.startsWith("docs:") ? (
          <DocsPage target={selected.slice(5)} />
        ) : selected === "projects" && teamMode ? (
          <ProjectsPage />
        ) : selected === "account" && teamMode ? (
          <AccountPage />
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
        <span className="engine">
          <span className={`dot ${health.isSuccess ? "ok" : health.isError ? "bad" : ""}`} aria-hidden />
          {health.isSuccess ? `Engine ${health.data.version}` : health.isError ? "Engine unreachable. Is pyro serve still running?" : "Connecting…"}
        </span>
        <span className="status-host">{window.location.host}</span>
        {ws.data && <span className="path-hint mono">{ws.data.root}</span>}
        <ThemeSelect />
      </footer>
    </div>
  );
}

const ENV_DOTS = ["#52c41a", "#faad14", "#1677ff", "#722ed1", "#13c2c2", "#eb2f96"];

/** A stable colour per environment name, so each one is easy to recognise. */
function envDot(name: string) {
  if (/prod/i.test(name)) return "#cf1920";
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return ENV_DOTS[h % ENV_DOTS.length];
}

function TopBar({ workspace }: { workspace?: string }) {
  const envs = useQuery({ queryKey: ["environments"], queryFn: api.environments });
  const environment = useApp((s) => s.environment);
  const setEnvironment = useApp((s) => s.setEnvironment);
  const select = useApp((s) => s.select);
  const setReport = useApp((s) => s.setReport);
  const teamMode = useTeam((s) => s.me !== null);

  // A remembered environment may have been deleted on disk since.
  const envValue = environment && envs.data?.includes(environment) ? environment : "";
  const go = (where: string | null) => {
    setReport(null);
    select(where);
  };

  return (
    <header className="topbar">
      <button className="brand" onClick={() => go(null)} aria-label="Pyro home">
        <img src={logo} alt="" width={22} height={28} />
        <span>Pyro</span>
      </button>
      {teamMode ? (
        <>
          <span className="topbar-divider" aria-hidden />
          <ProjectSwitcher />
        </>
      ) : (
        workspace && (
          <>
            <span className="topbar-divider" aria-hidden />
            <span className="ws-name" title="Workspace">
              <Briefcase size={16} />
              {workspace}
            </span>
          </>
        )
      )}
      <div className={teamMode ? "topbar-right has-user" : "topbar-right"}>
        <label className="env-select">
          <span className="env-label">Environment</span>
          <Select
            aria-label="Environment"
            value={envValue}
            onChange={(v) => setEnvironment(v || null)}
            popupMatchSelectWidth={false}
            options={[
              { value: "", label: <EnvOption name="No environment" color="#d9d9d9" /> },
              ...(envs.data ?? []).map((n) => ({ value: n, label: <EnvOption name={n} color={envDot(n)} /> })),
            ]}
          />
        </label>
        <Tooltip title="Environments and globals">
          <Button type="text" icon={<SlidersHorizontal size={16} />} onClick={() => go(envValue ? `vars:env:${envValue}` : "vars:globals")} aria-label="Edit environments and globals" />
        </Tooltip>
        <Tooltip title="Cookies">
          <Button type="text" className="topbar-extra" icon={<Cookie size={16} />} onClick={() => go("cookies")} aria-label="Manage cookies" />
        </Tooltip>
        <Tooltip title="User guide">
          <Button type="text" className="topbar-extra" icon={<BookOpen size={16} />} onClick={() => go("docs")} aria-label="Docs" />
        </Tooltip>
        <Button icon={<FileInput size={14} />} onClick={() => go("import")} aria-label="Import">
          Import
        </Button>
        <UserMenu />
      </div>
    </header>
  );
}

function EnvOption({ name, color }: { name: string; color: string }) {
  return (
    <span className="env-option">
      <span className="env-dot" style={{ background: color }} aria-hidden />
      {name}
    </span>
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
  const { message: notify } = AntApp.useApp();

  if (q.isPending) {
    return (
      <div className="work-pad">
        <section className="card">
          <Skeleton active paragraph={{ rows: 3 }} />
        </section>
      </div>
    );
  }
  if (q.error) {
    const gone = q.error instanceof ApiError && q.error.status === 404;
    return (
      <Blocker title={gone ? "This request no longer exists" : "Couldn't open the request"}>
        {gone ? "It may have been renamed or deleted on disk." : q.error.message}
        <div>
          <Button onClick={() => select(null)}>Close</Button>
        </div>
      </Blocker>
    );
  }
  const setDraft = useApp.getState().setDraft;
  const saveExample = (res: PyroResponse) => {
    let body = "";
    try {
      body = new TextDecoder("utf-8", { fatal: true }).decode(decodeBody(res.body));
    } catch {
      notify.warning("This response isn't text, so it can't be saved as an example.");
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
    <div className="work" style={{ gridTemplateRows: `${split.top}fr 16px ${100 - split.top}fr` }} ref={split.ref}>
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
      <section className="card editor">
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
    <div className="page">
      <section className="card report" aria-label="Import result">
        <header className="report-head">
          <CircleCheck size={22} className="ok-icon" />
          <div>
            <h1>
              {mode === "export"
                ? `Exported “${report.collection}” to Postman`
                : report.into
                  ? `Added ${report.requests} request${report.requests === 1 ? "" : "s"} to ${report.slug}`
                  : `Imported ${report.collection ? `“${report.collection}”` : "environment"}`}
            </h1>
            <p className="muted">
              <Tag>{report.format}</Tag>
              {report.slug && mode === "export" && (
                <>
                  {plural(report.requests, "request")} in {plural(report.folders, "folder")}, downloaded as <code>{report.slug}.postman_collection.json</code>
                </>
              )}
              {report.slug && mode === "import" && (
                <>
                  {report.into ? "" : `${plural(report.requests, "request")} in ${plural(report.folders, "folder")}, `}saved to <code>collections/{report.slug}</code>
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
                  <Button
                    type="link"
                    className="inline-link"
                    onClick={() => {
                      onClose();
                      select(`load:${l}`);
                    }}
                  >
                    {l}
                  </Button>
                </span>
              ))}
            </p>
          </div>
          <Button type="text" icon={<X size={16} />} onClick={onClose} aria-label="Close import result" />
        </header>

        {report.warnings.length === 0 ? (
          <Alert type="success" showIcon title={mode === "export" ? "Everything converted." : "Everything converted. Nothing needs your attention."} />
        ) : (
          <>
            <h2>
              {report.warnings.length} thing{report.warnings.length > 1 ? "s" : ""} to check
            </h2>
            <Table
              size="small"
              bordered
              pagination={false}
              rowKey={(_, i) => String(i)}
              dataSource={report.warnings}
              columns={[
                {
                  title: "Where",
                  key: "where",
                  width: "34%",
                  render: (_, w) =>
                    w.path && report.slug ? (
                      <Button
                        type="link"
                        className="inline-link mono"
                        onClick={() => {
                          setTab("collections");
                          select(`${report.slug}/${w.path}`);
                          onClose();
                        }}
                      >
                        {w.path}
                      </Button>
                    ) : (
                      "Whole collection"
                    ),
                },
                { title: "What happened", dataIndex: "detail", key: "detail" },
              ]}
            />
          </>
        )}
        <div className="report-actions">
          <Button type="primary" onClick={onClose}>
            Done
          </Button>
        </div>
      </section>
    </div>
  );
}

function Welcome({ hasCollections }: { hasCollections: boolean }) {
  return (
    <div className="page">
      <section className="card welcome">
        <img src={logo} alt="" width={44} height={56} />
        <h1>{hasCollections ? "Pick a request to start" : "Bring your API in"}</h1>
        <p className="muted">
          {hasCollections
            ? "Choose a request in the sidebar, or press + on a collection to add one. Send with Ctrl + Enter, save with Ctrl + S."
            : "Import a Postman collection or environment with Import in the top bar. Pyro writes it as YAML files you can commit."}
        </p>
        <p>
          <Button icon={<BookOpen size={14} />} onClick={() => useApp.getState().select("docs:getting-started")}>
            Read the user guide
          </Button>
        </p>
        <div className="welcome-legend">
          <h2>Reading response times</h2>
          <p className="muted">Each measured time carries a swatch that glows hotter as it gets slower, from cold steel to white heat.</p>
          <HeatLegend />
        </div>
      </section>
    </div>
  );
}

function Blocker({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="page">
      <section className="card blocker" role="alert">
        <Result status="warning" title={<h1>{title}</h1>} subTitle={<div className="blocker-body">{children}</div>} />
      </section>
    </div>
  );
}

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

const THEMES: { value: Theme; label: string }[] = [
  { value: "system", label: "System theme" },
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
];

function ThemeSelect() {
  const theme = useApp((s) => s.theme);
  const setTheme = useApp((s) => s.setTheme);
  return (
    <Dropdown
      trigger={["click"]}
      placement="topRight"
      menu={{
        selectable: true,
        selectedKeys: [theme],
        items: THEMES.map((t) => ({ key: t.value, label: t.label })),
        onClick: ({ key }) => setTheme(key as Theme),
      }}
    >
      <Button type="text" className="theme-switch" aria-label="Theme">
        {THEMES.find((t) => t.value === theme)?.label}
        <ChevronDown size={12} />
      </Button>
    </Dropdown>
  );
}
