import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderOpen, Layers, Plug, Plus, Radio, RefreshCw, Trash2 } from "lucide-react";
import { api, type TreeNode, type WorkspaceInfo } from "../api";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";

export function Sidebar({ ws }: { ws: WorkspaceInfo | undefined }) {
  const tab = useApp((s) => s.sidebarTab);
  const setTab = useApp((s) => s.setSidebarTab);
  return (
    <nav className="rail" aria-label="Workspace">
      <div className="rail-tabs" role="tablist">
        <button role="tab" aria-selected={tab === "collections"} onClick={() => setTab("collections")}>
          Collections
        </button>
        <button role="tab" aria-selected={tab === "history"} onClick={() => setTab("history")}>
          History
        </button>
      </div>
      {tab === "collections" ? <Collections ws={ws} /> : <History />}
    </nav>
  );
}

function Collections({ ws }: { ws: WorkspaceInfo | undefined }) {
  const qc = useQueryClient();
  const selected = useApp((s) => s.selected);
  const select = useApp((s) => s.select);
  const create = useMutation({
    mutationFn: async (parent: TreeNode) => {
      const taken = new Set((parent.children ?? []).map((c) => c.path));
      let slug = "new-request";
      for (let i = 2; taken.has(`${parent.path}/${slug}`); i++) slug = `new-request-${i}`;
      const path = `${parent.path}/${slug}`;
      await api.saveRequest(path, { kind: "http", name: "New request", method: "GET", url: "" });
      return path;
    },
    onSuccess: async (path) => {
      await qc.invalidateQueries({ queryKey: ["workspace"] });
      select(path);
    },
  });

  if (!ws) return <TreeSkeleton />;
  if (ws.collections.length === 0) {
    return (
      <div className="rail-empty">
        <p>No collections yet.</p>
        <p className="muted">Import a Postman collection from the top bar, or create a folder under <code>collections/</code> in the workspace.</p>
      </div>
    );
  }
  return (
    <div className="rail-scroll">
      <div className="rail-head">
        <span className="muted small">{ws.collections.length} collection{ws.collections.length > 1 ? "s" : ""}</span>
        <button className="icon-btn" onClick={() => qc.invalidateQueries({ queryKey: ["workspace"] })} aria-label="Reload from disk" title="Reload from disk">
          <RefreshCw size={13} />
        </button>
      </div>
      <ul className="tree" role="tree" aria-label="Collections">
        {ws.collections.map((n) => (
          <TreeItem key={n.path} node={n} depth={0} selected={selected} onSelect={select} onCreate={(p) => create.mutate(p)} />
        ))}
      </ul>
      {create.error && <p className="inline-error pad">Couldn't create the request: {create.error.message}</p>}
    </div>
  );
}

function TreeItem({
  node,
  depth,
  selected,
  onSelect,
  onCreate,
}: {
  node: TreeNode;
  depth: number;
  selected: string | null;
  onSelect: (p: string) => void;
  onCreate: (parent: TreeNode) => void;
}) {
  const containsSelected = !!selected && selected.startsWith(node.path + "/");
  const [open, setOpen] = useState(depth === 0 || containsSelected);
  const indent = { paddingLeft: 10 + depth * 14 };

  if (node.type === "request") {
    return (
      <li role="treeitem" aria-selected={selected === node.path}>
        <button className={`row ${selected === node.path ? "is-active" : ""}`} style={indent} onClick={() => onSelect(node.path)}>
          <KindBadge kind={node.kind} method={node.method} />
          <span className="row-label">{node.name}</span>
        </button>
      </li>
    );
  }

  const Icon = node.type === "collection" ? Layers : open ? FolderOpen : Folder;
  return (
    <li role="treeitem" aria-expanded={open}>
      <div className="row-wrap">
        <button className={`row ${node.type}`} style={indent} onClick={() => setOpen(!open)}>
          <ChevronRight size={13} className={`chev ${open ? "open" : ""}`} />
          <Icon size={14} className="row-icon" />
          <span className="row-label">{node.name}</span>
        </button>
        <button className="icon-btn row-action" onClick={() => onCreate(node)} aria-label={`New request in ${node.name}`} title="New request">
          <Plus size={14} />
        </button>
      </div>
      {open && node.children && node.children.length > 0 && (
        <ul role="group">
          {node.children.map((c) => (
            <TreeItem key={c.path} node={c} depth={depth + 1} selected={selected} onSelect={onSelect} onCreate={onCreate} />
          ))}
        </ul>
      )}
    </li>
  );
}

export function KindBadge({ kind, method }: { kind?: string; method?: string }) {
  if (kind === "websocket")
    return (
      <span className="badge stream" title="WebSocket">
        <Plug size={11} /> WS
      </span>
    );
  if (kind === "socketio")
    return (
      <span className="badge stream" title="Socket.IO">
        <Radio size={11} /> SIO
      </span>
    );
  const m = (method ?? "GET").toUpperCase();
  return <span className={`badge m-${m.toLowerCase()}`}>{m === "DELETE" ? "DEL" : m === "OPTIONS" ? "OPT" : m}</span>;
}

function History() {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const setResult = useApp((s) => s.setResult);
  const selected = useApp((s) => s.selected);
  const q = useQuery({ queryKey: ["history"], queryFn: () => api.history() });
  const clear = useMutation({ mutationFn: api.clearHistory, onSuccess: () => qc.invalidateQueries({ queryKey: ["history"] }) });

  const open = async (id: number, path?: string) => {
    const r = await api.historyEntry(id);
    const key = path || `history:${id}`;
    setResult(key, r);
    select(key);
  };

  if (q.isPending) return <TreeSkeleton />;
  if (q.error) return <p className="inline-error pad">{q.error.message}</p>;
  if (q.data.length === 0) {
    return (
      <div className="rail-empty">
        <p>Nothing sent yet.</p>
        <p className="muted">Every request you send is kept here, newest first.</p>
      </div>
    );
  }
  return (
    <div className="rail-scroll">
      <div className="rail-head">
        <span className="muted small">Last {q.data.length}</span>
        <button className="icon-btn" onClick={() => clear.mutate()} aria-label="Clear history" title="Clear history">
          <Trash2 size={13} />
        </button>
      </div>
      <ul className="history" aria-label="History">
        {q.data.map((h) => (
          <li key={h.id}>
            <button
              className={`hist-row ${selected === (h.path || `history:${h.id}`) ? "is-active" : ""}`}
              onClick={() => open(h.id, h.path)}
              title={`${h.method} ${h.url}`}
            >
              <span className="hist-top">
                <KindBadge method={h.method} />
                <span className="row-label">{h.name || h.url}</span>
              </span>
              <span className="hist-meta">
                {h.error ? (
                  <span className="hist-err">failed</span>
                ) : (
                  <>
                    <span className={`hist-status s${String(h.status)[0]}`}>{h.status}</span>
                    <span className={`heat-text-${heatOf(h.durationMs)}`}>{formatMs(h.durationMs)}</span>
                  </>
                )}
                <time dateTime={h.at}>{relativeTime(h.at)}</time>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function relativeTime(iso: string): string {
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString();
}

function TreeSkeleton() {
  return (
    <div className="pad" aria-busy="true" aria-label="Loading">
      {[70, 55, 80, 60, 45].map((w, i) => (
        <span key={i} className="skeleton line" style={{ width: `${w}%` }} />
      ))}
    </div>
  );
}
