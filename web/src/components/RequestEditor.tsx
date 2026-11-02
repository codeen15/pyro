import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Input, Segmented, Select, Space, Tabs, Tooltip } from "antd";
import { Check, Pencil, Save, SendHorizontal, Trash2 } from "../icons";
import { OAuth2Form, TokenPanel, type TokenContext } from "./OAuth2";
import { api, type Auth, type Body, type KeyValue, type PyroRequest } from "../api";
import { useApp } from "../store";
import { KVEditor } from "./KVEditor";
import { ProcessorList, TimerEditor, postList, preList, setPost, setPre } from "./Processors";
import { FieldsForm, KindLabel, usePluginTypes } from "./Plugins";
import { ExamplesEditor } from "./Examples";
import { useDeleteNode } from "./Sidebar";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
type Tab = "params" | "headers" | "body" | "options" | "auth" | "pre" | "post" | "examples";
const TABS: { id: Tab; label: string }[] = [
  { id: "params", label: "Params" },
  { id: "headers", label: "Headers" },
  { id: "body", label: "Body" },
  { id: "auth", label: "Auth" },
  { id: "pre", label: "Pre-processors" },
  { id: "post", label: "Post-processors" },
  { id: "examples", label: "Examples" },
];
// Plugin request types: the plugin's own fields replace params, and the
// body is sent as text.
const PLUGIN_TABS: { id: Tab; label: string }[] = [
  { id: "body", label: "Body" },
  { id: "options", label: "Options" },
  { id: "headers", label: "Headers" },
  { id: "auth", label: "Auth" },
  { id: "pre", label: "Pre-processors" },
  { id: "post", label: "Post-processors" },
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


  const pre = preList(req);
  const post = postList(req);
  const counts: Record<Tab, number> = {
    params: (req.query ?? []).filter((q) => !q.disabled).length,
    headers: (req.headers ?? []).filter((h) => !h.disabled).length,
    body: req.body && req.body.type !== "none" ? 1 : 0,
    auth: req.auth ? 1 : 0,
    options: Object.keys(req.options ?? {}).length,
    pre: pre.length + (req.think ? 1 : 0),
    post: post.length,
    examples: req.examples?.length ?? 0,
  };

  return (
    <section className="card editor">
      <RequestTitle req={req} path={path} dirty={dirty} onName={(name) => edit({ name })}>
        <KindLabel value={req.kind} />
        <SaveButton dirty={dirty} saving={save.isPending} onSave={() => save.mutate()} />
        <DeleteRequestButton path={path} name={saved.name} />
      </RequestTitle>
      {save.error && <Alert type="error" showIcon title={`Couldn't save: ${save.error.message}`} />}
      {pluginId && !provider && (
        <Alert
          type="warning"
          showIcon
          title={
            <>
              No installed plugin provides <code>{req.kind}</code>. Install it from the Plugins tab; the request is kept as written.
            </>
          }
        />
      )}
      {provider && !provider.plugin.ready && (
        <Alert
          type="warning"
          showIcon
          title={`The ${provider.plugin.title || provider.plugin.name} plugin is ${provider.plugin.approved ? "disabled" : "waiting for approval"}. Open it in the Plugins tab.`}
        />
      )}

      <form
        className="urlbar"
        onSubmit={(e) => {
          e.preventDefault();
          send();
        }}
      >
        <Space.Compact block size="large">
          {pluginId ? (
            <span className="kind-tag plugin-tag" title={provider?.plugin.title}>
              {(provider?.title || pluginId).toUpperCase().slice(0, 8)}
            </span>
          ) : (
            <Select
              className={`method-select m-${(req.method ?? "GET").toLowerCase()}`}
              value={req.method ?? "GET"}
              onChange={(method) => edit({ method })}
              aria-label="Method"
              popupMatchSelectWidth={false}
              options={METHODS.map((m) => ({ value: m, label: <span className={`method-opt m-${m.toLowerCase()}`}>{m}</span> }))}
            />
          )}
          <Input
            className="url-input mono"
            value={req.url}
            onChange={(e) => edit({ url: e.target.value })}
            placeholder={provider?.urlPlaceholder ?? (pluginId ? `${pluginId}://host:port` : "https://api.example.com/resource or {{baseUrl}}/path")}
            aria-label="URL"
            spellCheck={false}
            autoComplete="off"
          />
          <Button className="send" type="primary" htmlType="submit" loading={sending} disabled={!req.url.trim()} icon={<SendHorizontal size={15} />} title="Send (Ctrl+Enter)">
            {sending ? "Sending" : "Send"}
          </Button>
        </Space.Compact>
      </form>

      <Tabs
        className="part-tabs"
        activeKey={activeTab}
        onChange={(k) => setTab(k as Tab)}
        aria-label="Request parts"
        items={tabs.map(({ id: t, label }) => ({
          key: t,
          label: (
            <>
              {label}
              {counts[t] > 0 && !(pluginId && t === "body") && <span className="count">{t === "body" ? req.body!.type : t === "auth" ? req.auth!.type : counts[t]}</span>}
            </>
          ),
        }))}
      />

      <div className="tab-panel" role="tabpanel">
        {activeTab === "params" && <KVEditor label="Query parameters" rows={req.query ?? []} onChange={(query) => edit({ query })} />}
        {activeTab === "headers" && <KVEditor label="Headers" rows={req.headers ?? []} onChange={(headers) => edit({ headers })} keyPlaceholder="Header" />}
        {activeTab === "body" &&
          (pluginId ? (
            <Input.TextArea
              className="code-input mono"
              value={req.body?.content ?? ""}
              onChange={(e) => edit({ body: { type: "text", content: e.target.value } })}
              spellCheck={false}
              aria-label="Payload"
              autoSize={{ minRows: 6 }}
              placeholder="Payload sent by the plugin. Variables like {{name}} are filled in."
            />
          ) : (
            <BodyEditor body={req.body} onChange={(body) => edit({ body })} />
          ))}
        {activeTab === "options" && (
          <FieldsForm fields={provider?.fields ?? []} values={req.options} onChange={(options) => edit({ options: Object.keys(options).length ? options : undefined })} label="request type" />
        )}
        {activeTab === "auth" && <AuthEditor auth={req.auth} onChange={(auth) => edit({ auth })} context={{ path, request: req }} />}
        {activeTab === "examples" && <ExamplesEditor examples={req.examples ?? []} onChange={(examples) => edit({ examples: examples.length ? examples : undefined })} />}
        {activeTab === "pre" && (
          <div className="proc-tab">
            <section>
              <h3 className="sub-head">Think time</h3>
              <p className="muted small">Load tests and opted-in collection runs; manual sends don't wait.</p>
              <TimerEditor value={req.think} onChange={(think) => edit({ think })} inheritLabel="Inherit (folder/collection)" />
            </section>
            <section>
              <h3 className="sub-head">Pre-processors</h3>
              <ProcessorList phase="pre" items={pre} onChange={(items) => edit(setPre(req, items))} />
            </section>
          </div>
        )}
        {activeTab === "post" && (
          <div className="proc-tab">
            <section>
              <h3 className="sub-head">Post-processors</h3>
              <ProcessorList phase="post" items={post} onChange={(items) => edit(setPost(req, items))} />
            </section>
          </div>
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
        <label className="req-name-wrap">
          <input className="req-name" value={req.name} onChange={(e) => onName(e.target.value)} aria-label="Request name" />
          <Pencil className="req-name-pen" />
        </label>
        <span className="req-path mono">
          {path}
          {dirty && <span className="dirty" title="Unsaved changes"> · unsaved</span>}
        </span>
      </div>
      <div className="req-head-actions">{children}</div>
    </header>
  );
}

/** Deletes the open request (after confirming). */
export function DeleteRequestButton({ path, name }: { path: string; name: string }) {
  const del = useDeleteNode();
  return (
    <Tooltip title="Delete request">
      <Button icon={<Trash2 size={14} />} onClick={() => del({ type: "request", path, name })} aria-label={`Delete request ${name}`} />
    </Tooltip>
  );
}

/** Save / Saved, as in the reference design: red outline while there are unsaved edits. */
export function SaveButton({ dirty, saving, onSave }: { dirty: boolean; saving: boolean; onSave: () => void }) {
  return (
    <Button
      className={dirty ? "save-btn is-dirty" : "save-btn"}
      onClick={onSave}
      disabled={!dirty || saving}
      loading={saving}
      icon={dirty ? <Save size={14} /> : <Check size={14} />}
      title="Save (Ctrl+S)"
    >
      {saving ? "Saving" : dirty ? "Save" : "Saved"}
    </Button>
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
      <div className="body-type-row">
        <div className="seg-scroll">
          <Segmented
            aria-label="Body type"
            value={type}
            onChange={(v) => onChange(v === "none" ? undefined : { ...body, type: v as string })}
            options={BODY_TYPES.map((t) => ({ value: t.value, label: t.label }))}
          />
        </div>
        {type === "json" && body?.content && !jsonError && (
          <Button size="small" onClick={() => set({ content: JSON.stringify(JSON.parse(body.content!), null, 2) })}>
            Format
          </Button>
        )}
      </div>

      {type === "none" && <p className="muted">This request sends no body.</p>}
      {(type === "json" || type === "text" || type === "xml") && (
        <>
          <Input.TextArea
            className="code-input mono"
            value={body?.content ?? ""}
            onChange={(e) => set({ content: e.target.value })}
            spellCheck={false}
            aria-label="Body"
            autoSize={{ minRows: 8 }}
            placeholder={type === "json" ? '{\n  "key": "{{value}}"\n}' : ""}
          />
          {jsonError && <Alert type="warning" showIcon title={`Not valid JSON yet: ${jsonError}. It will be sent as written.`} />}
        </>
      )}
      {(type === "form" || type === "multipart") && (
        <KVEditor label="Form fields" rows={body?.fields ?? []} onChange={(fields: KeyValue[]) => set({ fields })} allowFiles={type === "multipart"} />
      )}
      {type === "graphql" && (
        <div className="split-2">
          <label>
            <span className="field-label">Query</span>
            <Input.TextArea className="code-input mono" value={body?.content ?? ""} onChange={(e) => set({ content: e.target.value })} spellCheck={false} autoSize={{ minRows: 8 }} />
          </label>
          <label>
            <span className="field-label">Variables (JSON)</span>
            <Input.TextArea className="code-input mono" value={body?.variables ?? ""} onChange={(e) => set({ variables: e.target.value })} spellCheck={false} autoSize={{ minRows: 8 }} />
          </label>
        </div>
      )}
      {type === "file" && (
        <label className="field-row">
          <span className="field-label">File path</span>
          <Input className="mono wide" value={body?.file ?? ""} onChange={(e) => set({ file: e.target.value })} placeholder="data/payload.bin (relative to the workspace)" />
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
  digest: [
    { key: "username", label: "Username" },
    { key: "password", label: "Password", secret: true },
  ],
  oauth2: [], // OAuth2Form
};

const AUTH_TYPES = [
  { value: "inherit", label: "Inherit from folder / collection" },
  { value: "none", label: "No auth" },
  { value: "basic", label: "Basic" },
  { value: "bearer", label: "Bearer token" },
  { value: "apikey", label: "API key" },
  { value: "digest", label: "Digest" },
  { value: "oauth2", label: "OAuth 2.0" },
];

export function AuthEditor({ auth, onChange, context }: { auth?: Auth; onChange: (a: Auth | undefined) => void; context?: TokenContext }) {
  const type = auth?.type ?? "inherit";
  const params = auth?.params ?? {};
  const fields = AUTH_FIELDS[type];
  const pluginAuths = usePluginTypes("auth");
  const pluginAuth = type.startsWith("plugin:") ? pluginAuths.find((a) => `plugin:${a.id}` === type) : undefined;
  const unknown = !AUTH_FIELDS[type] && !["inherit", "none"].includes(type) && !pluginAuth;

  return (
    <div className="auth-editor">
      <div className="field-row">
        <label className="field-label" htmlFor="auth-type">
          Type
        </label>
        <Select
          id="auth-type"
          className="auth-type"
          value={type}
          onChange={(v) => onChange(v === "inherit" ? undefined : { type: v, params: {} })}
          options={[
            ...AUTH_TYPES,
            ...(pluginAuths.length > 0 ? [{ label: "Plugins", options: pluginAuths.map((a) => ({ value: `plugin:${a.id}`, label: a.title || a.id })) }] : []),
            ...(unknown ? [{ value: type, label: `${type} (not available)` }] : []),
          ]}
        />
      </div>
      {pluginAuth && (
        <>
          {!pluginAuth.plugin.ready && <Alert type="warning" showIcon title={`The ${pluginAuth.plugin.title || pluginAuth.plugin.name} plugin isn't ready (see the Plugins tab).`} />}
          <FieldsForm fields={pluginAuth.fields ?? []} values={params} onChange={(p) => onChange({ type, params: p })} label="auth type" />
        </>
      )}
      {type === "inherit" && <p className="muted">Uses the nearest auth set on a parent folder or the collection.</p>}
      {type === "digest" && <p className="muted small">Answers the server's digest challenge (MD5 or SHA-256, qop auth); later requests to the same host reuse it without an extra round trip.</p>}
      {type === "inherit" && context?.path && <TokenPanel context={context} inheritedOnly />}
      {type === "oauth2" && <OAuth2Form params={params} onChange={(p) => onChange({ type, params: p })} context={context} />}
      {fields?.map((f) => {
        const Field = f.secret ? Input.Password : Input;
        return (
          <label key={f.key} className="field-row">
            <span className="field-label">{f.label}</span>
            <Field
              className="mono wide"
              value={params[f.key] ?? ""}
              onChange={(e) => onChange({ type, params: { ...params, [f.key]: e.target.value } })}
              placeholder={f.secret ? "{{secret}}" : ""}
              autoComplete="off"
              spellCheck={false}
            />
          </label>
        );
      })}
      {type === "apikey" && (
        <div className="field-row">
          <span className="field-label">Add to</span>
          <Select
            aria-label="Add API key to"
            value={params.in ?? "header"}
            onChange={(v) => onChange({ type, params: { ...params, in: v } })}
            options={[
              { value: "header", label: "Header" },
              { value: "query", label: "Query parameter" },
            ]}
          />
        </div>
      )}
    </div>
  );
}
