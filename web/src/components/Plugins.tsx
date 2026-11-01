import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, FileUp, Plus, Puzzle, ShieldAlert, ShieldCheck, Trash2, TriangleAlert } from "lucide-react";
import { api, type Kind, type PluginField, type PluginInfo, type PluginProvide } from "../api";
import { useApp } from "../store";

export function usePlugins() {
  return useQuery({ queryKey: ["plugins"], queryFn: api.plugins, staleTime: 10_000 });
}

/** Installed protocol/auth types (ready or not), for editors. */
export function usePluginTypes(kind: "protocol" | "auth") {
  const q = usePlugins();
  const out: (PluginProvide & { plugin: PluginInfo })[] = [];
  for (const p of q.data?.plugins ?? []) for (const pr of p.provides ?? []) if (pr.kind === kind) out.push({ ...pr, plugin: p });
  return out;
}

export function pluginStatus(p: PluginInfo): { label: string; tone: "ok" | "warn" | "error" | "muted" } {
  if (p.error) return { label: "Broken", tone: "error" };
  if (!p.enabled) return { label: "Disabled", tone: "muted" };
  if (!p.approved) return { label: "Needs approval", tone: "warn" };
  return { label: "Ready", tone: "ok" };
}

/** Renders inputs for plugin-declared fields. */
export function FieldsForm({
  fields,
  values,
  onChange,
  label,
}: {
  fields: PluginField[];
  values: Record<string, string> | undefined;
  onChange: (v: Record<string, string>) => void;
  label: string;
}) {
  const vals = values ?? {};
  const set = (k: string, v: string) => {
    const next = { ...vals, [k]: v };
    if (v === "") delete next[k];
    onChange(next);
  };
  if (fields.length === 0) return <p className="muted small">This {label} has no settings.</p>;
  return (
    <div className="fields-form">
      {fields.map((f) => {
        const id = `pf-${label}-${f.key}`;
        const v = vals[f.key] ?? "";
        const title = f.label || f.key;
        let input: React.ReactNode;
        switch (f.type) {
          case "select":
            input = (
              <select id={id} value={v || f.default || ""} onChange={(e) => set(f.key, e.target.value)}>
                {(f.options ?? []).map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
            );
            break;
          case "boolean":
            input = (
              <label className="check">
                <input id={id} type="checkbox" checked={(v || f.default) === "true"} onChange={(e) => set(f.key, e.target.checked ? "true" : "false")} />
                {f.help ?? "On"}
              </label>
            );
            break;
          case "text":
            input = <textarea id={id} className="code-input mono small-area" value={v} placeholder={f.default} onChange={(e) => set(f.key, e.target.value)} spellCheck={false} />;
            break;
          default:
            input = (
              <input
                id={id}
                className="cell mono wide"
                type={f.type === "secret" ? "password" : "text"}
                inputMode={f.type === "number" ? "numeric" : undefined}
                value={v}
                placeholder={f.default ?? (f.type === "secret" ? "{{secret}}" : "")}
                onChange={(e) => set(f.key, e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            );
        }
        return (
          <div key={f.key} className="field-row field-top">
            <label className="field-label" htmlFor={id}>
              {title}
            </label>
            <div className="field-body">
              {input}
              {f.help && f.type !== "boolean" && <p className="muted small field-help">{f.help}</p>}
            </div>
          </div>
        );
      })}
    </div>
  );
}

/** The request type selector: built-in kinds plus installed plugin protocols. */
export function KindSelect({ value, onChange }: { value: Kind; onChange: (k: Kind) => void }) {
  const protos = usePluginTypes("protocol");
  const known = ["http", "websocket", "socketio", ...protos.map((p) => `plugin:${p.id}`)].includes(value);
  return (
    <select className="kind-select" value={value} onChange={(e) => onChange(e.target.value as Kind)} aria-label="Request type" title="Request type">
      <option value="http">HTTP</option>
      <option value="websocket">WebSocket</option>
      <option value="socketio">Socket.IO</option>
      {protos.length > 0 && (
        <optgroup label="Plugins">
          {protos.map((p) => (
            <option key={p.id} value={`plugin:${p.id}`}>
              {p.title || p.id}
            </option>
          ))}
        </optgroup>
      )}
      {!known && <option value={value}>{value} (not installed)</option>}
    </select>
  );
}

// --- Rail -------------------------------------------------------------------

export function PluginsRail() {
  const q = usePlugins();
  const select = useApp((s) => s.select);
  const selected = useApp((s) => s.selected);
  return (
    <div className="rail-scroll">
      <div className="rail-head">
        <span className="muted small">{q.data ? `${q.data.plugins.length} installed` : ""}</span>
        <button className="icon-btn" onClick={() => select("plugins:install")} aria-label="Install a plugin" title="Install a plugin">
          <Plus size={14} />
        </button>
      </div>
      {q.data && !q.data.available && <p className="pad inline-warn">Plugins are unavailable: the plugin folder couldn't be opened.</p>}
      {q.data?.plugins.length === 0 && (
        <div className="rail-empty">
          <p>No plugins yet.</p>
          <p className="muted">Plugins add request types (like raw TCP) and auth types. Press + to install one.</p>
        </div>
      )}
      <ul className="tree">
        {q.data?.plugins.map((p) => {
          const st = pluginStatus(p);
          return (
            <li key={p.name}>
              <button className={`hist-row ${selected === `plugin:${p.name}` ? "is-active" : ""}`} onClick={() => select(`plugin:${p.name}`)}>
                <span className="hist-top">
                  <Puzzle size={13} className="row-icon" />
                  <span className="row-label">{p.title || p.name}</span>
                </span>
                <span className="hist-meta">
                  <span className={`tone-${st.tone}`}>{st.label}</span>
                  <span>{p.version}</span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// --- Plugin page --------------------------------------------------------------

export function PluginPage({ name }: { name: string }) {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const q = usePlugins();
  const p = q.data?.plugins.find((x) => x.name === name);
  const [settings, setSettings] = useState<Record<string, string> | null>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const refresh = (info?: PluginInfo) => {
    if (info) qc.setQueryData(["plugins"], (old: typeof q.data) => (old ? { ...old, plugins: old.plugins.map((x) => (x.name === info.name ? info : x)) } : old));
    qc.invalidateQueries({ queryKey: ["plugins"] });
  };
  const approve = useMutation({ mutationFn: () => api.approvePlugin(name), onSuccess: refresh });
  const toggle = useMutation({ mutationFn: (on: boolean) => api.enablePlugin(name, on), onSuccess: refresh });
  const save = useMutation({
    mutationFn: () => api.configurePlugin(name, settings ?? {}),
    onSuccess: (info) => {
      setSettings(null);
      refresh(info);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.removePlugin(name),
    onSuccess: () => {
      refresh();
      select(null);
    },
  });

  if (q.isPending) return <p className="pad muted">Loading…</p>;
  if (!p) return <p className="pad muted">This plugin isn't installed.</p>;
  const st = pluginStatus(p);
  const net = p.capabilities?.network ?? [];
  const err = approve.error || toggle.error || save.error || remove.error;

  return (
    <section className="plugin-page">
      <header className="lt-head">
        <div>
          <h1>
            {p.title || p.name}
            <span className={`verdict tone-${st.tone}`}>{st.label}</span>
          </h1>
          <p className="muted small">
            {p.name} {p.version}
            {p.author && <> · {p.author}</>}
            {p.license && <> · {p.license}</>}
          </p>
        </div>
        <div className="lt-actions">
          <label className="check">
            <input type="checkbox" checked={p.enabled} onChange={(e) => toggle.mutate(e.target.checked)} />
            Enabled
          </label>
        </div>
      </header>
      {p.description && <p className="plugin-desc">{p.description}</p>}
      {p.error && (
        <div className="notice error">
          <TriangleAlert size={15} />
          <span>{p.error}</span>
        </div>
      )}
      {err && <p className="inline-error">{(err as Error).message}</p>}

      <section className={`caps ${p.approved ? "is-approved" : "is-pending"}`}>
        {p.approved ? <ShieldCheck size={18} /> : <ShieldAlert size={18} />}
        <div>
          <h2>Capabilities</h2>
          {net.length === 0 ? (
            <p>No network access. It can only work with the data Pyro passes it.</p>
          ) : (
            <>
              <p>It may open connections to:</p>
              <ul className="cap-list">
                {net.map((n) => (
                  <li key={n} className="mono">
                    {n === "*:*" ? "*:* (any host and port)" : n}
                  </li>
                ))}
              </ul>
            </>
          )}
          {p.approved ? (
            <p className="muted small">
              <Check size={12} /> Approved. A new version asking for anything different needs approval again.
            </p>
          ) : (
            <>
              <p className="muted small">Plugins run sandboxed and can't touch files or other hosts. Approve only plugins you trust.</p>
              <button className="btn primary" onClick={() => approve.mutate()} disabled={approve.isPending}>
                Approve these capabilities
              </button>
            </>
          )}
        </div>
      </section>

      <h2 className="sub-head">Adds</h2>
      <ul className="provides">
        {p.provides.map((pr) => (
          <li key={pr.kind + pr.id}>
            <span className="badge stream mono">{pr.kind === "protocol" ? "REQUEST TYPE" : "AUTH TYPE"}</span>
            <strong>{pr.title || pr.id}</strong>
            <code>plugin:{pr.id}</code>
            {pr.fields && pr.fields.length > 0 && <span className="muted small">fields: {pr.fields.map((f) => f.label || f.key).join(", ")}</span>}
          </li>
        ))}
      </ul>
      <p className="muted small">
        {p.provides.some((pr) => pr.kind === "protocol") && "Use it by choosing its type in a request's Type menu. "}
        {p.provides.some((pr) => pr.kind === "auth") && "Its auth type appears in the Auth tab of requests, folders and collections."}
      </p>

      {(p.config?.length ?? 0) > 0 && (
        <>
          <h2 className="sub-head">Settings</h2>
          <FieldsForm fields={p.config ?? []} values={settings ?? p.settings} onChange={setSettings} label="plugin" />
          <button className="btn ghost" onClick={() => save.mutate()} disabled={settings === null || save.isPending}>
            Save settings
          </button>
        </>
      )}

      <div className="danger-zone">
        {confirmRemove ? (
          <>
            <span>Remove {p.title || p.name}? Requests using it will stop working until it's installed again.</span>
            <button className="btn ghost" onClick={() => remove.mutate()}>
              <Trash2 size={13} /> Remove
            </button>
            <button className="btn ghost" onClick={() => setConfirmRemove(false)}>
              Keep it
            </button>
          </>
        ) : (
          <button className="btn ghost" onClick={() => setConfirmRemove(true)}>
            <Trash2 size={13} /> Remove plugin
          </button>
        )}
      </div>
    </section>
  );
}

// --- Install -------------------------------------------------------------------

export function InstallPlugin() {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const list = usePlugins();
  const fileRef = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const search = useQuery({
    queryKey: ["plugin-index", query],
    queryFn: () => api.searchPlugins(query),
    enabled: !!list.data?.indexConfigured,
  });

  const done = async (name: string) => {
    await qc.invalidateQueries({ queryKey: ["plugins"] });
    select(`plugin:${name}`);
  };
  const upload = async (f: File) => {
    setBusy(true);
    setError(null);
    try {
      const info = await api.installPlugin(f);
      await done(info.name);
    } catch (e) {
      setError(`${f.name}: ${(e as Error).message}`);
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  return (
    <section className="plugin-page">
      <header className="lt-head">
        <div>
          <h1>Install a plugin</h1>
          <p className="muted small">After installing, you'll review what the plugin may connect to before it can run.</p>
        </div>
      </header>
      <div className="install-box">
        <FileUp size={20} />
        <div>
          <p>
            <strong>From a file.</strong> A <code>.pyro-plugin</code> or <code>.zip</code> archive holding <code>pyro-plugin.yaml</code> and the module.
          </p>
          <p className="muted small">
            Build one with <code>pyro plugin pack &lt;dir&gt;</code>. From a folder, use the CLI: <code>pyro plugin install &lt;dir&gt;</code>.
          </p>
          <button className="btn primary" onClick={() => fileRef.current?.click()} disabled={busy}>
            {busy ? "Installing…" : "Choose archive"}
          </button>
          <input ref={fileRef} type="file" accept=".pyro-plugin,.zip,application/zip" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        </div>
      </div>
      {error && <pre className="inline-error mono pre-wrap">{error}</pre>}

      <h2 className="sub-head">From the plugin index</h2>
      {!list.data?.indexConfigured ? (
        <p className="muted small">
          No index configured. Start Pyro with <code>PYRO_PLUGIN_INDEX=&lt;url of index.json&gt;</code> to browse one. A public index isn't published yet.
        </p>
      ) : (
        <>
          <input className="cell wide" placeholder="Search plugins" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search plugins" />
          {search.error && <p className="inline-error">{search.error.message}</p>}
          <ul className="index-list">
            {search.data?.map((e) => (
              <li key={e.name}>
                <div>
                  <strong>{e.title || e.name}</strong> <span className="muted small">{e.version}</span>
                  {e.description && <p className="muted small">{e.description}</p>}
                </div>
                <button
                  className="btn ghost small"
                  disabled={busy}
                  onClick={async () => {
                    setBusy(true);
                    setError(null);
                    try {
                      await done((await api.installFromIndex(e.name, e.version)).name);
                    } catch (err) {
                      setError((err as Error).message);
                    } finally {
                      setBusy(false);
                    }
                  }}
                >
                  Install
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
