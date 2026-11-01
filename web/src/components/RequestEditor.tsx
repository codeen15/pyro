import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleAlert, Save, SendHorizontal } from "lucide-react";
import { OAuth2Form, TokenPanel, type TokenContext } from "./OAuth2";
import { api, type Auth, type Body, type KeyValue, type PyroRequest } from "../api";
import { useApp } from "../store";
import { KVEditor } from "./KVEditor";
import { ScriptsEditor, TestsEditor } from "./TestsEditor";
import { FieldsForm, KindSelect, usePluginTypes } from "./Plugins";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
type Tab = "params" | "headers" | "body" | "options" | "auth" | "scripts" | "tests";
const TABS: { id: Tab; label: string }[] = [
  { id: "params", label: "Params" },
  { id: "headers", label: "Headers" },
  { id: "body", label: "Body" },
  { id: "auth", label: "Auth" },
  { id: "scripts", label: "Scripts" },
  { id: "tests", label: "Tests" },
];
// Plugin request types: the plugin's own fields replace params, and the
// body is sent as text.
const PLUGIN_TABS: { id: Tab; label: string }[] = [
  { id: "body", label: "Body" },
  { id: "options", label: "Options" },
  { id: "headers", label: "Headers" },
  { id: "auth", label: "Auth" },
  { id: "scripts", label: "Scripts" },
  { id: "tests", label: "Tests" },
];

export function RequestEditor({ path, saved }: { path: string; saved: PyroRequest }) {
  const qc = useQueryClient();
  const draft = useApp((s) => s.drafts[path]);
  const setDraft = useApp((s) => s.setDraft);
  const clearDraft = useApp((s) => s.clearDraft);
  const environment = useApp((s) => s.environment);
  const sending = useApp((s) => !!s.sending[path]);
  const setSending = useApp((s) => s.setSending);
  const setResult = useApp((s) => s.setResult);
  const [tab, setTab] = useState<Tab>("params");
  const protocols = usePluginTypes("protocol");

  const req = draft ?? saved;
  const dirty = draft !== undefined;
  const edit = (patch: Partial<PyroRequest>) => setDraft(path, { ...req, ...patch });

  const save = useMutation({
    mutationFn: () => api.saveRequest(path, req),
    onSuccess: (r) => {
      qc.setQueryData(["request", path], r);
      clearDraft(path);
      qc.invalidateQueries({ queryKey: ["workspace"] });
    },
  });

  const send = async () => {
    if (sending) return;
    setSending(path, true);
    try {
      const res = await api.send({ path, request: req, environment: environment ?? undefined });
      setResult(path, res);
    } catch (e) {
      setResult(path, { error: (e as Error).message, startedAt: new Date().toISOString() });
    } finally {
      setSending(path, false);
      qc.invalidateQueries({ queryKey: ["history"] });
    }
  };

  // Ctrl/Cmd+Enter sends, Ctrl/Cmd+S saves.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return;
      if (e.key === "Enter") {
        e.preventDefault();
        send();
      } else if (e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (dirty) save.mutate();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const pluginId = req.kind.startsWith("plugin:") ? req.kind.slice(7) : null;
  const provider = pluginId ? protocols.find((p) => p.id === pluginId) : undefined;
  const tabs = pluginId ? PLUGIN_TABS : TABS;
  const activeTab = tabs.some((t) => t.id === tab) ? tab : tabs[0].id;
  const changeKind = (kind: PyroRequest["kind"]) => {
    const next: PyroRequest = { ...req, kind };
    if (kind.startsWith("plugin:") && !next.body) next.body = { type: "text", content: "" };
    setDraft(path, next);
  };

  const counts: Record<Tab, number> = {
    params: (req.query ?? []).filter((q) => !q.disabled).length,
    headers: (req.headers ?? []).filter((h) => !h.disabled).length,
    body: req.body && req.body.type !== "none" ? 1 : 0,
    auth: req.auth ? 1 : 0,
    options: Object.keys(req.options ?? {}).length,
    scripts: (req.scripts?.pre?.trim() ? 1 : 0) + (req.scripts?.post?.trim() ? 1 : 0),
    tests: (req.assertions?.length ?? 0) + (req.extract?.length ?? 0),
  };

  return (
    <section className="editor">
      <RequestTitle req={req} path={path} dirty={dirty} onName={(name) => edit({ name })}>
        <KindSelect value={req.kind} onChange={changeKind} />
        <button className="btn ghost" onClick={() => save.mutate()} disabled={!dirty || save.isPending} title="Save (Ctrl+S)">
          <Save size={14} />
          {save.isPending ? "Saving…" : dirty ? "Save" : "Saved"}
        </button>
      </RequestTitle>
      {save.error && <p className="inline-error">Couldn't save: {save.error.message}</p>}
      {pluginId && !provider && (
        <div className="notice warn">
          <CircleAlert size={15} />
          <span>
            No installed plugin provides <code>{req.kind}</code>. Install it from the Plugins tab; the request is kept as written.
          </span>
        </div>
      )}
      {provider && !provider.plugin.ready && (
        <div className="notice warn">
          <CircleAlert size={15} />
          <span>
            The {provider.plugin.title || provider.plugin.name} plugin is {provider.plugin.approved ? "disabled" : "waiting for approval"}. Open it in the Plugins tab.
          </span>
        </div>
      )}

      <form
        className="urlbar"
        onSubmit={(e) => {
          e.preventDefault();
          send();
        }}
      >
        {pluginId ? (
          <span className="kind-tag plugin-tag" title={provider?.plugin.title}>
            {(provider?.title || pluginId).toUpperCase().slice(0, 8)}
          </span>
        ) : (
          <select
            className={`method m-${(req.method ?? "GET").toLowerCase()}`}
            value={req.method ?? "GET"}
            onChange={(e) => edit({ method: e.target.value })}
            aria-label="Method"
          >
            {METHODS.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        )}
        <input
          className="url-input mono"
          value={req.url}
          onChange={(e) => edit({ url: e.target.value })}
          placeholder={provider?.urlPlaceholder ?? (pluginId ? `${pluginId}://host:port` : "https://api.example.com/resource or {{baseUrl}}/path")}
          aria-label="URL"
          spellCheck={false}
          autoComplete="off"
        />
        <button className="btn primary send" type="submit" disabled={sending || !req.url.trim()} title="Send (Ctrl+Enter)">
          <SendHorizontal size={15} />
          {sending ? "Sending…" : "Send"}
        </button>
      </form>

      <div className="tabs" role="tablist" aria-label="Request parts">
        {tabs.map(({ id: t, label }) => (
          <button key={t} role="tab" aria-selected={activeTab === t} className="tab" onClick={() => setTab(t)}>
            {label}
            {counts[t] > 0 && !(pluginId && t === "body") && <span className="count">{t === "body" ? req.body!.type : t === "auth" ? req.auth!.type : counts[t]}</span>}
          </button>
        ))}
      </div>

      <div className="tab-panel" role="tabpanel">
        {activeTab === "params" && <KVEditor label="Query parameters" rows={req.query ?? []} onChange={(query) => edit({ query })} />}
        {activeTab === "headers" && <KVEditor label="Headers" rows={req.headers ?? []} onChange={(headers) => edit({ headers })} keyPlaceholder="Header" />}
        {activeTab === "body" &&
          (pluginId ? (
            <textarea
              className="code-input mono"
              value={req.body?.content ?? ""}
              onChange={(e) => edit({ body: { type: "text", content: e.target.value } })}
              spellCheck={false}
              aria-label="Payload"
              placeholder="Payload sent by the plugin. Variables like {{name}} are filled in."
            />
          ) : (
            <BodyEditor body={req.body} onChange={(body) => edit({ body })} />
          ))}
        {activeTab === "options" && (
          <FieldsForm fields={provider?.fields ?? []} values={req.options} onChange={(options) => edit({ options: Object.keys(options).length ? options : undefined })} label="request type" />
        )}
        {activeTab === "auth" && <AuthEditor auth={req.auth} onChange={(auth) => edit({ auth })} context={{ path, request: req }} />}
        {activeTab === "scripts" && <ScriptsEditor scripts={req.scripts} onChange={(scripts) => edit({ scripts })} />}
        {activeTab === "tests" && (
          <TestsEditor
            assertions={req.assertions ?? []}
            extract={req.extract ?? []}
            onAssertions={(assertions) => edit({ assertions })}
            onExtract={(extract) => edit({ extract })}
          />
        )}
      </div>
    </section>
  );
}

export function RequestTitle({
  req,
  path,
  dirty,
  onName,
  children,
}: {
  req: PyroRequest;
  path: string;
  dirty: boolean;
  onName: (n: string) => void;
  children?: React.ReactNode;
}) {
  return (
    <header className="req-head">
      <div className="req-head-text">
        <input className="req-name" value={req.name} onChange={(e) => onName(e.target.value)} aria-label="Request name" />
        <span className="req-path mono">
          {path}
          {dirty && <span className="dirty" title="Unsaved changes"> · unsaved</span>}
        </span>
      </div>
      {children}
    </header>
  );
}

const BODY_TYPES: { value: string; label: string }[] = [
  { value: "none", label: "None" },
  { value: "json", label: "JSON" },
  { value: "text", label: "Text" },
  { value: "xml", label: "XML" },
  { value: "form", label: "Form URL-encoded" },
  { value: "multipart", label: "Multipart form" },
  { value: "graphql", label: "GraphQL" },
  { value: "file", label: "Binary file" },
];

function BodyEditor({ body, onChange }: { body?: Body; onChange: (b: Body | undefined) => void }) {
  const type = body?.type ?? "none";
  const set = (patch: Partial<Body>) => onChange({ type, ...body, ...patch });
  const jsonError = type === "json" && body?.content ? jsonProblem(body.content) : null;

  return (
    <div className="body-editor">
      <div className="field-row">
        <label className="field-label" htmlFor="body-type">
          Type
        </label>
        <select id="body-type" value={type} onChange={(e) => onChange(e.target.value === "none" ? undefined : { ...body, type: e.target.value })}>
          {BODY_TYPES.map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </select>
        {type === "json" && body?.content && !jsonError && (
          <button className="btn ghost small" onClick={() => set({ content: JSON.stringify(JSON.parse(body.content!), null, 2) })}>
            Format
          </button>
        )}
      </div>

      {type === "none" && <p className="muted">This request sends no body.</p>}
      {(type === "json" || type === "text" || type === "xml") && (
        <>
          <textarea
            className="code-input mono"
            value={body?.content ?? ""}
            onChange={(e) => set({ content: e.target.value })}
            spellCheck={false}
            aria-label="Body"
            placeholder={type === "json" ? '{\n  "key": "{{value}}"\n}' : ""}
          />
          {jsonError && <p className="inline-warn">Not valid JSON yet: {jsonError}. It will be sent as written.</p>}
        </>
      )}
      {(type === "form" || type === "multipart") && (
        <KVEditor label="Form fields" rows={body?.fields ?? []} onChange={(fields: KeyValue[]) => set({ fields })} allowFiles={type === "multipart"} />
      )}
      {type === "graphql" && (
        <div className="split-2">
          <label>
            <span className="field-label">Query</span>
            <textarea className="code-input mono" value={body?.content ?? ""} onChange={(e) => set({ content: e.target.value })} spellCheck={false} />
          </label>
          <label>
            <span className="field-label">Variables (JSON)</span>
            <textarea className="code-input mono" value={body?.variables ?? ""} onChange={(e) => set({ variables: e.target.value })} spellCheck={false} />
          </label>
        </div>
      )}
      {type === "file" && (
        <label className="field-row">
          <span className="field-label">File path</span>
          <input className="cell mono wide" value={body?.file ?? ""} onChange={(e) => set({ file: e.target.value })} placeholder="data/payload.bin (relative to the workspace)" />
        </label>
      )}
    </div>
  );
}

function jsonProblem(s: string): string | null {
  try {
    JSON.parse(s);
    return null;
  } catch (e) {
    // Variables like {{id}} make otherwise-valid JSON unparsable; don't nag about those.
    if (/\{\{[^}]+\}\}/.test(s)) return null;
    return (e as Error).message.replace(/^JSON\.parse: /, "");
  }
}

const AUTH_FIELDS: Record<string, { key: string; label: string; secret?: boolean }[]> = {
  basic: [
    { key: "username", label: "Username" },
    { key: "password", label: "Password", secret: true },
  ],
  bearer: [{ key: "token", label: "Token", secret: true }],
  apikey: [
    { key: "key", label: "Key name" },
    { key: "value", label: "Value", secret: true },
  ],
  oauth2: [], // OAuth2Form
};

export function AuthEditor({ auth, onChange, context }: { auth?: Auth; onChange: (a: Auth | undefined) => void; context?: TokenContext }) {
  const type = auth?.type ?? "inherit";
  const params = auth?.params ?? {};
  const fields = AUTH_FIELDS[type];
  const pluginAuths = usePluginTypes("auth");
  const pluginAuth = type.startsWith("plugin:") ? pluginAuths.find((a) => `plugin:${a.id}` === type) : undefined;

  return (
    <div className="auth-editor">
      <div className="field-row">
        <label className="field-label" htmlFor="auth-type">
          Type
        </label>
        <select id="auth-type" value={type} onChange={(e) => onChange(e.target.value === "inherit" ? undefined : { type: e.target.value, params: {} })}>
          <option value="inherit">Inherit from folder / collection</option>
          <option value="none">No auth</option>
          <option value="basic">Basic</option>
          <option value="bearer">Bearer token</option>
          <option value="apikey">API key</option>
          <option value="oauth2">OAuth 2.0</option>
          {pluginAuths.length > 0 && (
            <optgroup label="Plugins">
              {pluginAuths.map((a) => (
                <option key={a.id} value={`plugin:${a.id}`}>
                  {a.title || a.id}
                </option>
              ))}
            </optgroup>
          )}
          {!AUTH_FIELDS[type] && !["inherit", "none"].includes(type) && !pluginAuth && <option value={type}>{type} (not available)</option>}
        </select>
      </div>
      {pluginAuth && (
        <>
          {!pluginAuth.plugin.ready && <p className="inline-warn">The {pluginAuth.plugin.title || pluginAuth.plugin.name} plugin isn't ready (see the Plugins tab).</p>}
          <FieldsForm fields={pluginAuth.fields ?? []} values={params} onChange={(p) => onChange({ type, params: p })} label="auth type" />
        </>
      )}
      {type === "inherit" && <p className="muted">Uses the nearest auth set on a parent folder or the collection.</p>}
      {type === "inherit" && context?.path && <TokenPanel context={context} inheritedOnly />}
      {type === "oauth2" && <OAuth2Form params={params} onChange={(p) => onChange({ type, params: p })} context={context} />}
      {fields?.map((f) => (
        <label key={f.key} className="field-row">
          <span className="field-label">{f.label}</span>
          <input
            className="cell mono wide"
            type={f.secret ? "password" : "text"}
            value={params[f.key] ?? ""}
            onChange={(e) => onChange({ type, params: { ...params, [f.key]: e.target.value } })}
            placeholder={f.secret ? "{{secret}}" : ""}
            autoComplete="off"
            spellCheck={false}
          />
        </label>
      ))}
      {type === "apikey" && (
        <label className="field-row">
          <span className="field-label">Add to</span>
          <select value={params.in ?? "header"} onChange={(e) => onChange({ type, params: { ...params, in: e.target.value } })}>
            <option value="header">Header</option>
            <option value="query">Query parameter</option>
          </select>
        </label>
      )}
    </div>
  );
}
