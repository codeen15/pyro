import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderOpen, Layers, Plug, Radio, RefreshCw, TriangleAlert } from "lucide-react";
import { api, ApiError, type PyroRequest, type TreeNode } from "./api";

export function App() {
  const [selected, setSelected] = useState<string | null>(null);
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, refetchInterval: 10_000 });
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });

  return (
    <div className="shell">
      <header className="topbar">
        <span className="brand">
          <FlameMark />
          Pyro
        </span>
        {ws.data && <span className="ws-name">{ws.data.name}</span>}
      </header>

      <nav className="sidebar" aria-label="Collections">
        <div className="sidebar-head">
          <span>Collections</span>
          <button className="icon-btn" onClick={() => ws.refetch()} aria-label="Reload workspace" title="Reload workspace">
            <RefreshCw size={14} className={ws.isFetching ? "spin" : undefined} />
          </button>
        </div>
        {ws.isPending && <p className="muted pad">Loading workspace…</p>}
        {ws.error && <ErrorNote error={ws.error} />}
        {ws.data && ws.data.collections.length === 0 && (
          <p className="muted pad">No collections yet. Add a folder under <code>collections/</code> in {ws.data.root}.</p>
        )}
        {ws.data && (
          <ul className="tree" role="tree">
            {ws.data.collections.map((n) => (
              <TreeItem key={n.path} node={n} depth={0} selected={selected} onSelect={setSelected} />
            ))}
          </ul>
        )}
      </nav>

      <main className="main">{selected ? <RequestView path={selected} /> : <EmptyMain />}</main>

      <footer className="statusbar">
        <span className={`dot ${health.isSuccess ? "ok" : health.isError ? "bad" : ""}`} aria-hidden />
        {health.isSuccess ? `Engine ${health.data.version}` : health.isError ? "Engine unreachable" : "Connecting…"}
        {ws.data && <span className="muted path-hint">{ws.data.root}</span>}
      </footer>
    </div>
  );
}

function TreeItem({
  node,
  depth,
  selected,
  onSelect,
}: {
  node: TreeNode;
  depth: number;
  selected: string | null;
  onSelect: (p: string) => void;
}) {
  const [open, setOpen] = useState(depth === 0);
  const indent = { paddingLeft: 8 + depth * 14 };

  if (node.type === "request") {
    return (
      <li role="treeitem" aria-selected={selected === node.path}>
        <button className={`row ${selected === node.path ? "active" : ""}`} style={indent} onClick={() => onSelect(node.path)}>
          <KindBadge kind={node.kind} method={node.method} />
          <span className="row-label">{node.name}</span>
        </button>
      </li>
    );
  }

  const FolderIcon = node.type === "collection" ? Layers : open ? FolderOpen : Folder;
  return (
    <li role="treeitem" aria-expanded={open}>
      <button className={`row ${node.type}`} style={indent} onClick={() => setOpen(!open)}>
        <ChevronRight size={14} className={`chev ${open ? "open" : ""}`} />
        <FolderIcon size={14} className="folder-icon" />
        <span className="row-label">{node.name}</span>
      </button>
      {open && node.children && node.children.length > 0 && (
        <ul role="group">
          {node.children.map((c) => (
            <TreeItem key={c.path} node={c} depth={depth + 1} selected={selected} onSelect={onSelect} />
          ))}
        </ul>
      )}
    </li>
  );
}

function KindBadge({ kind, method }: { kind?: string; method?: string }) {
  if (kind === "websocket")
    return (
      <span className="badge ws" title="WebSocket">
        <Plug size={12} /> WS
      </span>
    );
  if (kind === "socketio")
    return (
      <span className="badge sio" title="Socket.IO">
        <Radio size={12} /> SIO
      </span>
    );
  const m = (method ?? "GET").toUpperCase();
  return <span className={`badge m-${m.toLowerCase()}`}>{m === "DELETE" ? "DEL" : m}</span>;
}

function RequestView({ path }: { path: string }) {
  const q = useQuery({ queryKey: ["request", path], queryFn: () => api.request(path) });
  if (q.isPending) return <p className="muted pad">Loading…</p>;
  if (q.error) return <ErrorNote error={q.error} />;
  const r: PyroRequest = q.data;
  const isStream = r.kind === "websocket" || r.kind === "socketio";

  return (
    <article className="request">
      <h1 className="req-title">{r.name}</h1>
      <p className="muted req-path">{path}</p>

      <div className="urlbar">
        <KindBadge kind={r.kind} method={r.method} />
        <code className="url">{r.url}</code>
      </div>

      {r.description && <p className="desc">{r.description}</p>}

      {!isStream && <KVSection title="Query" rows={r.query} />}
      <KVSection title="Headers" rows={r.headers} />
      {r.auth && (
        <Section title="Auth">
          <p>
            <code>{r.auth.type}</code>
          </p>
        </Section>
      )}
      {r.body && r.body.type !== "none" && (
        <Section title={`Body · ${r.body.type}`}>
          {r.body.content ? <pre className="code">{r.body.content}</pre> : <KVTable rows={r.body.fields ?? []} />}
        </Section>
      )}
      {isStream && r.messages && r.messages.length > 0 && (
        <Section title="Saved messages">
          {r.messages.map((m, i) => (
            <div key={i} className="message">
              {m.event && <span className="event">{m.event}</span>}
              <pre className="code">{m.content}</pre>
            </div>
          ))}
        </Section>
      )}
    </article>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="section">
      <h2>{title}</h2>
      {children}
    </section>
  );
}

function KVSection({ title, rows }: { title: string; rows?: { key: string; value: string; disabled?: boolean }[] }) {
  if (!rows || rows.length === 0) return null;
  return (
    <Section title={title}>
      <KVTable rows={rows} />
    </Section>
  );
}

function KVTable({ rows }: { rows: { key: string; value: string; disabled?: boolean }[] }) {
  return (
    <table className="kv">
      <tbody>
        {rows.map((r, i) => (
          <tr key={i} className={r.disabled ? "disabled" : undefined}>
            <td>{r.key}</td>
            <td>{r.value}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function EmptyMain() {
  return (
    <div className="empty">
      <FlameMark size={40} />
      <h1>Pick a request</h1>
      <p className="muted">Choose a request from the sidebar to see what it sends. Editing and sending come in the next build.</p>
    </div>
  );
}

function ErrorNote({ error }: { error: Error }) {
  const unauthorized = error instanceof ApiError && error.status === 401;
  return (
    <div className="error-note" role="alert">
      <TriangleAlert size={16} />
      <span>
        {unauthorized ? (
          <>
            This tab has no session token. Open the link that <code>pyro serve</code> printed in your terminal.
          </>
        ) : (
          error.message
        )}
      </span>
    </div>
  );
}

function FlameMark({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden className="flame">
      <path d="M16 3c2 5 8 8 8 15a8 8 0 0 1-16 0c0-4 2-6 4-8 0 3 1 5 3 5-1-5 0-9 1-12z" fill="currentColor" />
    </svg>
  );
}
