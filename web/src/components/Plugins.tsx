import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Checkbox, Empty, Input, Select, Switch, Tag } from "antd";
import { FileUp, Plus, Puzzle, ShieldAlert, ShieldCheck, Trash2, TriangleAlert, Upload } from "../icons";
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
              <Select
                id={id}
                className="fields-select"
                value={v || f.default || undefined}
                onChange={(val: string) => set(f.key, val)}
                options={(f.options ?? []).map((o) => ({ value: o, label: o }))}
              />
            );
            break;
          case "boolean":
            input = (
              <Checkbox id={id} checked={(v || f.default) === "true"} onChange={(e) => set(f.key, e.target.checked ? "true" : "false")}>
                {f.help ?? "On"}
              </Checkbox>
            );
            break;
          case "text":
            input = <Input.TextArea id={id} className="mono" autoSize={{ minRows: 3, maxRows: 12 }} value={v} placeholder={f.default} onChange={(e) => set(f.key, e.target.value)} spellCheck={false} />;
            break;
          default: {
            const props = {
              id,
              className: "mono",
              value: v,
              placeholder: f.default ?? (f.type === "secret" ? "{{secret}}" : ""),
              onChange: (e: React.ChangeEvent<HTMLInputElement>) => set(f.key, e.target.value),
              autoComplete: "off",
              spellCheck: false,
            };
            input = f.type === "secret" ? <Input.Password {...props} /> : <Input {...props} inputMode={f.type === "number" ? "numeric" : undefined} />;
          }
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
/** The request type, fixed once the request exists (shown in the request header). */
export function KindLabel({ value }: { value: Kind }) {
  const protos = usePluginTypes("protocol");
  const plugin = value.startsWith("plugin:") ? protos.find((p) => `plugin:${p.id}` === value) : undefined;
  const label =
    value === "http" ? "HTTP" : value === "websocket" ? "WebSocket" : value === "socketio" ? "Socket.IO" : plugin ? plugin.title || plugin.id : `${value} (not installed)`;
  return (
    <span className={`kind-label k-${value.startsWith("plugin:") ? "plugin" : value}`} title="Request type (fixed when the request was created)">
      {label}
    </span>
  );
}

// --- Rail -------------------------------------------------------------------

export function PluginsRail() {
  const q = usePlugins();
  const select = useApp((s) => s.select);
  const selected = useApp((s) => s.selected);
  return (
    <div className="rail-scroll">
      <div className="rail-cta">
        <Button type="dashed" block icon={<Plus size={13} />} onClick={() => select("plugins:install")} aria-label="Install a plugin" title="Install a plugin">
          Install plugin
        </Button>
      </div>
      {q.data && !q.data.available && (
        <div className="pad">
          <Alert type="warning" showIcon title="Plugins are unavailable: the plugin folder couldn't be opened." />
        </div>
      )}
      {q.data?.plugins.length === 0 && (
        <div className="rail-empty">
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No plugins yet">
            <p className="muted">Plugins add request types (like raw TCP) and auth types. Install one to get started.</p>
          </Empty>
        </div>
      )}
      {(q.data?.plugins.length ?? 0) > 0 && (
        <div className="rail-head">
          <span className="rail-section">{q.data!.plugins.length} installed</span>
        </div>
      )}
      <ul className="tree">
        {q.data?.plugins.map((p) => {
          const st = pluginStatus(p);
          return (
            <li key={p.name}>
              <button className={`row plugin-row ${selected === `plugin:${p.name}` ? "is-active" : ""}`} onClick={() => select(`plugin:${p.name}`)}>
                <Puzzle size={14} className="row-icon" />
                <span className="row-label">{p.title || p.name}</span>
                {st.tone !== "ok" && <TriangleAlert size={13} className={`plugin-row-status tone-${st.tone}`} aria-label={st.label} />}
                <span className="plugin-row-version mono">{p.version}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// --- Plugin page --------------------------------------------------------------

const TONE_COLOR: Record<ReturnType<typeof pluginStatus>["tone"], string> = { ok: "success", warn: "warning", error: "error", muted: "default" };

export function PluginPage({ name }: { name: string }) {
  const qc = useQueryClient();
  const { modal } = App.useApp();
  const select = useApp((s) => s.select);
  const q = usePlugins();
  const p = q.data?.plugins.find((x) => x.name === name);
  const [settings, setSettings] = useState<Record<string, string> | null>(null);
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
  if (!p)
    return (
      <div className="page">
        <Empty className="plugin-missing" description="This plugin isn't installed." />
      </div>
    );
  const st = pluginStatus(p);
  const net = p.capabilities?.network ?? [];
  const err = approve.error || toggle.error || save.error || remove.error;
  const title = p.title || p.name;
  const confirmRemove = () =>
    modal.confirm({
      title: `Remove ${title}?`,
      content: "Requests using it will stop working until it's installed again.",
      okText: "Remove",
      okButtonProps: { danger: true },
      cancelText: "Keep it",
      onOk: () => remove.mutateAsync(),
    });

  return (
    <div className="page">
      <section className="card plugin-card">
        <header className="plugin-head">
          <span className="plugin-icon" aria-hidden>
            <Puzzle size={24} />
          </span>
          <div className="plugin-title">
            <h1>
              {title}
              <Tag className="mono">{p.version}</Tag>
              <Tag color={TONE_COLOR[st.tone]}>{st.label}</Tag>
            </h1>
            {p.description && <p className="plugin-desc">{p.description}</p>}
            <p className="muted small">
              {p.name}
              {p.author && <> · {p.author}</>}
              {p.license && <> · {p.license}</>}
              {p.homepage && (
                <>
                  {" "}·{" "}
                  <a href={p.homepage} target="_blank" rel="noreferrer noopener">
                    Homepage
                  </a>
                </>
              )}
            </p>
          </div>
          <label className="plugin-enable">
            <Switch checked={p.enabled} loading={toggle.isPending} onChange={(on) => toggle.mutate(on)} />
            Enabled
          </label>
        </header>

        {p.error && <Alert type="error" showIcon title={p.error} />}
        {err && <Alert type="error" showIcon title={(err as Error).message} />}
        {!p.approved && (
          <Alert
            className="caps"
            type="warning"
            showIcon
            icon={<ShieldAlert size={18} />}
            title="Approve its capabilities before it can run"
            description={
              <>
                <p>Plugins run sandboxed and can't touch files or other hosts. Approve only plugins you trust.</p>
                <Button type="primary" onClick={() => approve.mutate()} loading={approve.isPending}>
                  Approve these capabilities
                </Button>
              </>
            }
          />
        )}

        <dl className="plugin-defs">
          <dt>Hooks</dt>
          <dd>
            <ul className="provides">
              {p.provides.map((pr) => (
                <li key={pr.kind + pr.id}>
                  <Tag className="tag-accent">
                    {pr.kind === "protocol" ? "Request type" : "Auth type"}: {pr.title || pr.id}
                  </Tag>
                  <code>plugin:{pr.id}</code>
                  {pr.fields && pr.fields.length > 0 && <span className="muted small">fields: {pr.fields.map((f) => f.label || f.key).join(", ")}</span>}
                </li>
              ))}
            </ul>
            <p className="muted small">
              {p.provides.some((pr) => pr.kind === "protocol") && "Use it by choosing its type in a request's Type menu. "}
              {p.provides.some((pr) => pr.kind === "auth") && "Its auth type appears in the Auth tab of requests, folders and collections."}
            </p>
          </dd>

          <dt>Permissions</dt>
          <dd>
            {net.length === 0 ? (
              <>
                <Tag>No network access</Tag>
                <p className="muted small">It can only work with the data Pyro passes it.</p>
              </>
            ) : (
              <>
                <p className="small">It may open connections to:</p>
                <div className="cap-tags">
                  {net.map((n) => (
                    <Tag key={n} className="mono">
                      {n === "*:*" ? "*:* (any host and port)" : n}
                    </Tag>
                  ))}
                </div>
              </>
            )}
            {p.approved && (
              <p className="muted small caps-approved">
                <ShieldCheck size={13} /> Approved. A new version asking for anything different needs approval again.
              </p>
            )}
          </dd>

          <dt>Module</dt>
          <dd className="mono">{p.module}</dd>

          <dt>ABI</dt>
          <dd>v{p.abi}</dd>
        </dl>

        {(p.config?.length ?? 0) > 0 && (
          <div className="plugin-settings">
            <h2 className="dash-title">Settings</h2>
            <FieldsForm fields={p.config ?? []} values={settings ?? p.settings} onChange={setSettings} label="plugin" />
          </div>
        )}

        <footer className="plugin-foot">
          {(p.config?.length ?? 0) > 0 && (
            <Button type="primary" onClick={() => save.mutate()} disabled={settings === null} loading={save.isPending}>
              Save settings
            </Button>
          )}
          <Button danger icon={<Trash2 size={13} />} onClick={confirmRemove} className="plugin-remove">
            Remove plugin
          </Button>
        </footer>
      </section>
    </div>
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
    <div className="page">
      <section className="card plugin-card">
        <header className="plugin-head">
          <span className="plugin-icon" aria-hidden>
            <Puzzle size={24} />
          </span>
          <div className="plugin-title">
            <h1>Install a plugin</h1>
            <p className="muted">After installing, you'll review what the plugin may connect to before it can run.</p>
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
            <Button type="primary" icon={<Upload size={14} />} onClick={() => fileRef.current?.click()} loading={busy}>
              {busy ? "Installing…" : "Choose archive"}
            </Button>
            <input ref={fileRef} type="file" accept=".pyro-plugin,.zip,application/zip" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
          </div>
        </div>
        {error && <Alert type="error" showIcon title={<pre className="mono pre-wrap">{error}</pre>} />}

        <h2 className="dash-title">From the plugin index</h2>
        {!list.data?.indexConfigured ? (
          <p className="muted small">
            No index configured. Start Pyro with <code>PYRO_PLUGIN_INDEX=&lt;url of index.json&gt;</code> to browse one. A public index isn't published yet.
          </p>
        ) : (
          <>
            <Input.Search placeholder="Search plugins" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search plugins" allowClear loading={search.isFetching} />
            {search.error && <Alert type="error" showIcon title={search.error.message} />}
            {search.data && search.data.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No plugins match" />}
            <ul className="index-list">
              {search.data?.map((e) => (
                <li key={e.name}>
                  <div>
                    <strong>{e.title || e.name}</strong> <span className="muted small mono">{e.version}</span>
                    {e.description && <p className="muted small">{e.description}</p>}
                  </div>
                  <Button
                    size="small"
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
                  </Button>
                </li>
              ))}
            </ul>
          </>
        )}
      </section>
    </div>
  );
}
