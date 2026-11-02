import { useMemo, useState } from "react";
import { App, Button, Dropdown, Empty, Input, Modal, Skeleton, Tabs, Tooltip } from "antd";
import { MoreOutlined, SearchOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, BookOpen, ChevronRight, Download, Settings2, Folder, FolderOpen, Gauge, Layers, Play, Plus, RefreshCw, TriangleAlert } from "../icons";
import { PluginsRail } from "./Plugins";
import { api, saveFile, type TreeNode, type WorkspaceInfo } from "../api";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";
import { slugify } from "../loadProfiles";

export function Sidebar({ ws }: { ws: WorkspaceInfo | undefined }) {
  const tab = useApp((s) => s.sidebarTab);
  const setTab = useApp((s) => s.setSidebarTab);
  return (
    <nav className="rail" aria-label="Workspace">
      <Tabs
        className="rail-tabs"
        tabBarGutter={18}
        activeKey={tab}
        onChange={(k) => setTab(k as typeof tab)}
        items={[
          { key: "collections", label: "Collections" },
          { key: "history", label: "History" },
          { key: "load", label: "Load" },
          { key: "plugins", label: "Plugins" },
        ]}
      />
      {tab === "collections" ? <Collections ws={ws} /> : tab === "history" ? <History /> : tab === "load" ? <LoadTests /> : <PluginsRail />}
    </nav>
  );
}

/** Keeps requests whose name or path matches, with their folders. */
function filterTree(nodes: TreeNode[], q: string): TreeNode[] {
  if (!q) return nodes;
  const out: TreeNode[] = [];
  for (const n of nodes) {
    if (n.type === "request") {
      if (n.name.toLowerCase().includes(q) || n.path.toLowerCase().includes(q)) out.push(n);
      continue;
    }
    const children = filterTree(n.children ?? [], q);
    if (children.length > 0 || n.name.toLowerCase().includes(q)) out.push({ ...n, children: children.length > 0 ? children : n.children });
  }
  return out;
}

function countRequests(nodes: TreeNode[]): number {
  return nodes.reduce((sum, n) => sum + (n.type === "request" ? 1 : countRequests(n.children ?? [])), 0);
}

function Collections({ ws }: { ws: WorkspaceInfo | undefined }) {
  const qc = useQueryClient();
  const selected = useApp((s) => s.selected);
  const select = useApp((s) => s.select);
  const { message } = App.useApp();
  const [search, setSearch] = useState("");
  const [naming, setNaming] = useState(false);
  const [newName, setNewName] = useState("");
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

  const createCollection = async (name: string) => {
    const taken = new Set(ws?.collections.map((c) => c.path));
    let slug = slugify(name);
    for (let i = 2; taken.has(slug); i++) slug = `${slugify(name)}-${i}`;
    try {
      await api.createCollection(slug, name);
      await qc.invalidateQueries({ queryKey: ["workspace"] });
      select(`settings:${slug}`);
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const newCollection = () => {
    setNewName("");
    setNaming(true);
  };
  const nameModal = (
    <Modal
      title="New collection"
      open={naming}
      okText="Create"
      okButtonProps={{ disabled: !newName.trim() }}
      onOk={async () => {
        await createCollection(newName.trim());
        setNaming(false);
      }}
      onCancel={() => setNaming(false)}
      destroyOnHidden
    >
      <Input
        autoFocus
        aria-label="New collection name"
        placeholder="Payments API"
        value={newName}
        onChange={(e) => setNewName(e.target.value)}
        onPressEnter={async () => {
          if (!newName.trim()) return;
          await createCollection(newName.trim());
          setNaming(false);
        }}
      />
    </Modal>
  );

  const q = search.trim().toLowerCase();
  const nodes = useMemo(() => filterTree(ws?.collections ?? [], q), [ws, q]);

  if (!ws) return <TreeSkeleton />;
  if (ws.collections.length === 0) {
    return (
      <div className="rail-empty">
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No collections yet">
          <p className="muted">Import one (Postman, OpenAPI, HAR, cURL or JMeter) from the top bar, or start an empty one.</p>
          <Button icon={<Plus size={13} />} onClick={newCollection}>
            New collection
          </Button>
        </Empty>
        {nameModal}
      </div>
    );
  }
  const total = countRequests(ws.collections);
  return (
    <div className="rail-scroll">
      <div className="rail-search">
        <Input
          allowClear
          prefix={<SearchOutlined />}
          placeholder="Search requests"
          aria-label="Search requests"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Tooltip title="New collection">
          <Button icon={<Plus size={14} />} onClick={newCollection} aria-label="New collection" />
        </Tooltip>
        <Tooltip title="Reload from disk">
          <Button icon={<RefreshCw size={13} />} onClick={() => qc.invalidateQueries({ queryKey: ["workspace"] })} aria-label="Reload from disk" />
        </Tooltip>
      </div>
      <div className="rail-meta">
        {plural(ws.collections.length, "collection")} · {plural(total, "request")}
      </div>
      {nodes.length === 0 ? (
        <p className="rail-meta">No requests match “{search.trim()}”.</p>
      ) : (
        <ul className="tree" role="tree" aria-label="Collections">
          {nodes.map((n) => (
            <TreeItem key={n.path} node={n} depth={0} selected={selected} onSelect={select} onCreate={(p) => create.mutate(p)} forceOpen={!!q} />
          ))}
        </ul>
      )}
      {create.error && <p className="inline-error pad">Couldn't create the request: {create.error.message}</p>}
      {nameModal}
    </div>
  );
}

function TreeItem({
  node,
  depth,
  selected,
  onSelect,
  onCreate,
  forceOpen,
}: {
  node: TreeNode;
  depth: number;
  selected: string | null;
  onSelect: (p: string) => void;
  onCreate: (parent: TreeNode) => void;
  forceOpen?: boolean;
}) {
  const containsSelected = !!selected && selected.startsWith(node.path + "/");
  const [open, setOpen] = useState(depth === 0 || containsSelected);
  const indent = { paddingLeft: 8 + depth * 18 };
  const expanded = open || !!forceOpen;

  if (node.type === "request") {
    return (
      <li role="treeitem" aria-selected={selected === node.path}>
        <button className={`row ${selected === node.path ? "is-active" : ""}`} style={{ paddingLeft: 8 + depth * 18 + 22 }} onClick={() => onSelect(node.path)}>
          <KindBadge kind={node.kind} method={node.method} />
          <span className="row-label">{node.name}</span>
        </button>
      </li>
    );
  }

  const Icon = expanded ? FolderOpen : Folder;
  return (
    <li role="treeitem" aria-expanded={expanded}>
      <div className="row-wrap">
        <button className={`row ${node.type} ${selected === `run:${node.path}` ? "is-active" : ""}`} style={indent} onClick={() => setOpen(!expanded)}>
          <ChevronRight size={10} className={`chev ${expanded ? "open" : ""}`} />
          {node.type === "collection" ? <Layers size={14} className="row-icon" /> : <Icon size={14} className="row-icon" />}
          <span className="row-label">{node.name}</span>
        </button>
        <span className="row-actions">
          <Tooltip title="Run all requests">
            <Button type="text" size="small" icon={<Play size={12} />} onClick={() => onSelect(`run:${node.path}`)} aria-label={`Run ${node.name}`} />
          </Tooltip>
          <Tooltip title="New request">
            <Button type="text" size="small" icon={<Plus size={12} />} onClick={() => onCreate(node)} aria-label={`New request in ${node.name}`} />
          </Tooltip>
          <Tooltip title="Variables, auth and scripts">
            <Button type="text" size="small" icon={<Settings2 size={12} />} onClick={() => onSelect(`settings:${node.path}`)} aria-label={`Settings for ${node.name}`} />
          </Tooltip>
          {node.type === "collection" && <CollectionMenu slug={node.path} name={node.name} />}
        </span>
      </div>
      {expanded && node.children && node.children.length > 0 && (
        <ul role="group">
          {node.children.map((c) => (
            <TreeItem key={c.path} node={c} depth={depth + 1} selected={selected} onSelect={onSelect} onCreate={onCreate} forceOpen={forceOpen} />
          ))}
        </ul>
      )}
    </li>
  );
}

export function KindBadge({ kind, method }: { kind?: string; method?: string }) {
  if (kind === "websocket")
    return (
      <span className="badge m-ws" title="WebSocket">
        WS
      </span>
    );
  if (kind === "socketio")
    return (
      <span className="badge m-sio" title="Socket.IO">
        SIO
      </span>
    );
  if (kind?.startsWith("plugin:"))
    return (
      <span className="badge m-plugin" title={`Plugin request type ${kind}`}>
        {kind.slice(7, 11).toUpperCase()}
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
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Nothing sent yet">
          <p className="muted">Every request you send is kept here, newest first.</p>
        </Empty>
      </div>
    );
  }
  return (
    <div className="rail-scroll">
      <div className="rail-head">
        <span className="rail-section">Last {q.data.length}</span>
        <Button type="link" size="small" onClick={() => clear.mutate()} aria-label="Clear history">
          Clear
        </Button>
      </div>
      <ul className="history" aria-label="History">
        {q.data.map((h) => (
          <li key={h.id}>
            <button
              className={`hist-row ${selected === (h.path || `history:${h.id}`) ? "is-active" : ""}`}
              onClick={() => open(h.id, h.path)}
              title={`${h.method} ${h.url}`}
            >
              <KindBadge method={h.method} />
              <span className="hist-url mono">{h.name || h.url}</span>
              {h.error ? <span className="hist-err">failed</span> : <span className={`hist-status s${String(h.status)[0]}`}>{h.status}</span>}
              <span className="hist-meta">
                {!h.error && (
                  <span>
                    <span className={`swatch heat-${heatOf(h.durationMs)}`} aria-hidden />
                    {formatMs(h.durationMs)}
                  </span>
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

function LoadTests() {
  const select = useApp((s) => s.select);
  const selected = useApp((s) => s.selected);
  const tests = useQuery({ queryKey: ["loadtests"], queryFn: api.loadTests });
  const runs = useQuery({ queryKey: ["loadruns", ""], queryFn: () => api.loadRuns(), refetchInterval: 5000 });
  const running = runs.data?.find((r) => r.status === "running");

  return (
    <div className="rail-scroll">
      <div className="rail-cta">
        <Button type="dashed" block icon={<Plus size={13} />} onClick={() => select("load:new")} aria-label="New load test">
          New load test
        </Button>
      </div>
      {running && (
        <button className={`hist-row running-row ${selected === `loadrun:${running.id}` ? "is-active" : ""}`} onClick={() => select(`loadrun:${running.id}`)}>
          <Activity size={13} className="live-icon" />
          <span className="row-label">{running.name || running.test}</span>
          <span className="hist-meta">running now · open dashboard</span>
        </button>
      )}
      {tests.isPending && <TreeSkeleton />}
      {tests.data?.length === 0 && (
        <div className="rail-empty">
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No load tests yet">
            <p className="muted">
              Create one from a profile. It's saved as <code>loadtests/&lt;name&gt;.load.yaml</code>.
            </p>
          </Empty>
        </div>
      )}
      {(tests.data?.length ?? 0) > 0 && <div className="rail-section rail-head">Test plans</div>}
      <ul className="tree">
        {tests.data?.map((t) => (
          <li key={t.name}>
            <button className={`row ${selected === `load:${t.name}` ? "is-active" : ""}`} onClick={() => select(`load:${t.name}`)}>
              <Gauge size={14} className="row-icon" />
              <span className="row-label">{t.title || t.name}</span>
              {t.error && <TriangleAlert size={13} className="row-warn" aria-label="Invalid plan" />}
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
      <Skeleton active title={false} paragraph={{ rows: 5 }} />
    </div>
  );
}

/** Collection extras: generated API docs and Postman export. */
function CollectionMenu({ slug, name }: { slug: string; name: string }) {
  const setReport = useApp((s) => s.setReport);
  const { message } = App.useApp();
  const openDocs = async () => {
    try {
      const html = await api.collectionDocs(slug);
      const url = URL.createObjectURL(new Blob([html], { type: "text/html" }));
      window.open(url, "_blank", "noopener");
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const exportPostman = async () => {
    try {
      const ex = await api.exportCollection(slug);
      saveFile(ex.filename, JSON.stringify(ex.content, null, "\t"));
      if (ex.report && ex.report.warnings.length > 0) setReport({ mode: "export", data: ex.report });
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  return (
    <Dropdown
      trigger={["click"]}
      menu={{
        items: [
          { key: "docs", icon: <BookOpen size={13} />, label: `API docs for ${name}`, onClick: openDocs },
          { key: "export", icon: <Download size={13} />, label: `Export ${name} to Postman`, onClick: exportPostman },
        ],
      }}
    >
      <Button type="text" size="small" icon={<MoreOutlined />} aria-label={`More actions for ${name}`} />
    </Dropdown>
  );
}

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}
