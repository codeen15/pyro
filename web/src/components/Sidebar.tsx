import { useMemo, useState } from "react";
import { Alert, App, Button, Dropdown, Empty, Input, Modal, Skeleton, Tabs, Tooltip, type MenuProps } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, BookOpen, ChevronRight, Download, Folder, FolderOpen, FolderPlus, Gauge, Layers, MoreVertical, Play, Plus, RefreshCw, Search, Settings2, Trash2, TriangleAlert } from "../icons";
import { PluginsRail, usePluginTypes } from "./Plugins";
import { LoadTestMenu } from "./LoadActions";
import { api, saveFile, type Kind, type PyroRequest, type TreeNode, type WorkspaceInfo } from "../api";
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
  const [newIn, setNewIn] = useState<TreeNode | null>(null);
  const [folderIn, setFolderIn] = useState<TreeNode | null>(null);

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
          prefix={<Search size={14} />}
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
            <TreeItem key={n.path} node={n} depth={0} selected={selected} onSelect={select} onCreate={setNewIn} onNewFolder={setFolderIn} forceOpen={!!q} />
          ))}
        </ul>
      )}
      <NewRequestModal parent={newIn} onClose={() => setNewIn(null)} />
      <NewFolderModal parent={folderIn} onClose={() => setFolderIn(null)} />
      {nameModal}
    </div>
  );
}

const BUILTIN_KINDS: { kind: Kind; title: string; tag: string; desc: string }[] = [
  { kind: "http", title: "HTTP", tag: "GET", desc: "REST and GraphQL: method, params, headers, body and auth." },
  { kind: "websocket", title: "WebSocket", tag: "WS", desc: "A raw WebSocket connection. Send and watch text or binary messages." },
  { kind: "socketio", title: "Socket.IO", tag: "SIO", desc: "Socket.IO v4 client: namespaces, events and acks." },
];

/** Creates a request. Its type is chosen here and can't be changed later. */
function NewRequestModal({ parent, onClose }: { parent: TreeNode | null; onClose: () => void }) {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const protocols = usePluginTypes("protocol");
  const [kind, setKind] = useState<Kind>("http");
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: async () => {
      const title = name.trim() || "New request";
      const taken = new Set((parent!.children ?? []).map((c) => c.path));
      const base = slugify(title) || "new-request";
      let slug = base;
      for (let i = 2; taken.has(`${parent!.path}/${slug}`); i++) slug = `${base}-${i}`;
      const path = `${parent!.path}/${slug}`;
      const req: PyroRequest = { kind, name: title, url: "" };
      if (kind === "http") req.method = "GET";
      if (kind.startsWith("plugin:")) req.body = { type: "text", content: "" };
      await api.saveRequest(path, req);
      return path;
    },
    onSuccess: async (path) => {
      await qc.invalidateQueries({ queryKey: ["workspace"] });
      select(path);
      onClose();
    },
  });
  const options = [
    ...BUILTIN_KINDS,
    ...protocols.map((p) => ({
      kind: `plugin:${p.id}` as Kind,
      title: p.title || p.id,
      tag: p.id.slice(0, 4).toUpperCase(),
      desc: `Provided by the ${p.plugin.title || p.plugin.name} plugin.`,
    })),
  ];
  return (
    <Modal
      title={parent ? `New request in ${parent.name}` : "New request"}
      open={!!parent}
      okText="Create request"
      onOk={() => create.mutate()}
      confirmLoading={create.isPending}
      onCancel={onClose}
      afterOpenChange={(open) => {
        if (!open) {
          setKind("http");
          setName("");
          create.reset();
        }
      }}
      width={640}
      destroyOnHidden
    >
      <div className="kind-cards" role="radiogroup" aria-label="Request type">
        {options.map((o) => (
          <button
            key={o.kind}
            type="button"
            role="radio"
            aria-checked={kind === o.kind}
            className={`kind-card ${kind === o.kind ? "is-active" : ""}`}
            onClick={() => setKind(o.kind)}
            onDoubleClick={() => create.mutate()}
          >
            <span className={`kind-card-tag k-${o.kind.startsWith("plugin:") ? "plugin" : o.kind}`}>{o.tag}</span>
            <span className="kind-card-title">{o.title}</span>
            <span className="kind-card-desc">{o.desc}</span>
          </button>
        ))}
      </div>
      <label className="kind-name">
        <span className="field-label">Name</span>
        <Input autoFocus aria-label="Request name" placeholder="New request" value={name} onChange={(e) => setName(e.target.value)} onPressEnter={() => create.mutate()} />
      </label>
      <p className="muted small">The type can't be changed after the request is created.</p>
      {create.error && <Alert type="error" showIcon title={`Couldn't create the request: ${create.error.message}`} />}
    </Modal>
  );
}

function TreeItem({
  node,
  depth,
  selected,
  onSelect,
  onCreate,
  onNewFolder,
  forceOpen,
}: {
  node: TreeNode;
  depth: number;
  selected: string | null;
  onSelect: (p: string) => void;
  onCreate: (parent: TreeNode) => void;
  onNewFolder: (parent: TreeNode) => void;
  forceOpen?: boolean;
}) {
  const containsSelected = !!selected && selected.startsWith(node.path + "/");
  const [open, setOpen] = useState(depth === 0 || containsSelected);
  const indent = { paddingLeft: 8 + depth * 18 };
  const expanded = open || !!forceOpen;

  if (node.type === "request") {
    return (
      <li role="treeitem" aria-selected={selected === node.path}>
        <div className="row-wrap request-row">
          <button className={`row ${selected === node.path ? "is-active" : ""}`} style={{ paddingLeft: 8 + depth * 18 + 22 }} onClick={() => onSelect(node.path)}>
            <KindBadge kind={node.kind} method={node.method} />
            <span className="row-label">{node.name}</span>
          </button>
          <span className="row-actions">
            <NodeMenu node={node} onNewFolder={onNewFolder} />
          </span>
        </div>
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
            <Button type="text" icon={<Play size={12} />} onClick={() => onSelect(`run:${node.path}`)} aria-label={`Run ${node.name}`} />
          </Tooltip>
          <Tooltip title="New request">
            <Button type="text" icon={<Plus size={12} />} onClick={() => onCreate(node)} aria-label={`New request in ${node.name}`} />
          </Tooltip>
          <Tooltip title="Variables, auth and scripts">
            <Button type="text" icon={<Settings2 size={12} />} onClick={() => onSelect(`settings:${node.path}`)} aria-label={`Settings for ${node.name}`} />
          </Tooltip>
          <NodeMenu node={node} onNewFolder={onNewFolder} />
        </span>
      </div>
      {expanded && node.children && node.children.length > 0 && (
        <ul role="group">
          {node.children.map((c) => (
            <TreeItem key={c.path} node={c} depth={depth + 1} selected={selected} onSelect={onSelect} onCreate={onCreate} onNewFolder={onNewFolder} forceOpen={forceOpen} />
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
        <Button type="link" onClick={() => clear.mutate()} aria-label="Clear history">
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
            <div className="row-wrap request-row">
              <button className={`row ${selected === `load:${t.name}` ? "is-active" : ""}`} onClick={() => select(`load:${t.name}`)}>
                <Gauge size={14} className="row-icon" />
                <span className="row-label">{t.title || t.name}</span>
                {t.error && <TriangleAlert size={13} className="row-warn" aria-label="Invalid plan" />}
              </button>
              <span className="row-actions">
                <LoadTestMenu name={t.name} title={t.title || t.name} inRail />
              </span>
            </div>
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

function countIn(n: TreeNode): number {
  return n.type === "request" ? 1 : (n.children ?? []).reduce((sum, c) => sum + countIn(c), 0);
}

/** Asks, then deletes a request, folder or collection from disk and tidies the UI. */
export function useDeleteNode() {
  const qc = useQueryClient();
  const { modal, message } = App.useApp();
  return (node: Pick<TreeNode, "type" | "path" | "name" | "children">) => {
    const n = node.type === "request" ? 0 : countIn(node as TreeNode);
    const what = node.type === "request" ? "request" : node.type;
    modal.confirm({
      title: `Delete ${what} “${node.name}”?`,
      content:
        node.type === "request"
          ? `This deletes ${node.path}.req.yaml from the workspace.`
          : `This deletes the ${what} folder and ${n === 0 ? "everything in it" : `its ${n} request${n === 1 ? "" : "s"}`} from the workspace.`,
      okText: "Delete",
      okButtonProps: { danger: true },
      cancelText: "Keep it",
      onOk: async () => {
        try {
          if (node.type === "request") await api.deleteRequest(node.path);
          else if (node.type === "folder") await api.deleteFolder(node.path);
          else await api.deleteCollection(node.path);
        } catch (e) {
          message.error((e as Error).message);
          throw e;
        }
        const st = useApp.getState();
        const gone = (p: string | null) => !!p && (p === node.path || p.startsWith(node.path + "/") || p.endsWith(":" + node.path) || p.includes(":" + node.path + "/"));
        if (gone(st.selected)) st.select(null);
        for (const p of Object.keys(st.drafts)) if (gone(p)) st.clearDraft(p);
        qc.removeQueries({ queryKey: ["request", node.path] });
        await qc.invalidateQueries({ queryKey: ["workspace"] });
        message.success(`Deleted ${node.name}`);
      },
    });
  };
}

/** Row menu: new folder, docs and export (collections), delete. */
function NodeMenu({ node, onNewFolder }: { node: TreeNode; onNewFolder: (n: TreeNode) => void }) {
  const setReport = useApp((s) => s.setReport);
  const { message } = App.useApp();
  const del = useDeleteNode();
  const openDocs = async () => {
    try {
      const html = await api.collectionDocs(node.path);
      const url = URL.createObjectURL(new Blob([html], { type: "text/html" }));
      window.open(url, "_blank", "noopener");
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const exportPostman = async () => {
    try {
      const ex = await api.exportCollection(node.path);
      saveFile(ex.filename, JSON.stringify(ex.content, null, "\t"));
      if (ex.report && ex.report.warnings.length > 0) setReport({ mode: "export", data: ex.report });
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const items: NonNullable<MenuProps["items"]> = [];
  if (node.type !== "request") items.push({ key: "folder", icon: <FolderPlus size={14} />, label: `New folder in ${node.name}`, onClick: () => onNewFolder(node) });
  if (node.type === "collection") {
    items.push(
      { key: "docs", icon: <BookOpen size={14} />, label: `API docs for ${node.name}`, onClick: openDocs },
      { key: "export", icon: <Download size={14} />, label: `Export ${node.name} to Postman`, onClick: exportPostman },
    );
  }
  if (items.length) items.push({ type: "divider" });
  items.push({ key: "delete", icon: <Trash2 size={14} />, danger: true, label: `Delete ${node.name}`, onClick: () => del(node) });
  return (
    <Dropdown trigger={["click"]} menu={{ items }}>
      <Button type="text" icon={<MoreVertical size={14} />} aria-label={`More actions for ${node.name}`} />
    </Dropdown>
  );
}

/** Creates an empty folder inside a collection or folder. */
function NewFolderModal({ parent, onClose }: { parent: TreeNode | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: async () => {
      const title = name.trim();
      const taken = new Set((parent!.children ?? []).map((c) => c.path));
      const base = slugify(title) || "folder";
      let slug = base;
      for (let i = 2; taken.has(`${parent!.path}/${slug}`); i++) slug = `${base}-${i}`;
      await api.createFolder(`${parent!.path}/${slug}`, title);
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["workspace"] });
      onClose();
    },
  });
  return (
    <Modal
      title={parent ? `New folder in ${parent.name}` : "New folder"}
      open={!!parent}
      okText="Create folder"
      okButtonProps={{ disabled: !name.trim() }}
      onOk={() => create.mutate()}
      confirmLoading={create.isPending}
      onCancel={onClose}
      afterOpenChange={(open) => {
        if (!open) {
          setName("");
          create.reset();
        }
      }}
      destroyOnHidden
    >
      <label className="kind-name">
        <span className="field-label">Name</span>
        <Input autoFocus aria-label="Folder name" placeholder="Orders" value={name} onChange={(e) => setName(e.target.value)} onPressEnter={() => name.trim() && create.mutate()} />
      </label>
      <p className="muted small">Folders group requests and can carry their own variables, auth, think time and processors.</p>
      {create.error && <Alert type="error" showIcon title={`Couldn't create the folder: ${create.error.message}`} />}
    </Modal>
  );
}

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}
