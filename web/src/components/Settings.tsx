import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Input, Skeleton, Tabs, Tooltip } from "antd";
import { Check, Eye, EyeOff, Plus, RotateCcw, Save, Trash2, Upload } from "../icons";
import { api, type CollectionSettings, type Variable } from "../api";
import { AuthEditor } from "./RequestEditor";
import { ProcessorList, TimerEditor, postList, preList, setPost, setPre } from "./Processors";
import { MockPanel } from "./Mock";
import { useCanEdit } from "../team";
import { ReadOnlyBadge } from "./RequestEditor";
import { CurrentInput } from "./Variables";
import { VarScope } from "../vars";
import { useCurrentValues } from "../currentValues";

// Settings a collection or folder passes down to its requests: variables,
// auth, think time and pre/post processors (collection.yaml / folder.yaml).

type Tab = "overview" | "variables" | "auth" | "pre" | "post" | "mock";

export function SettingsPage({ path }: { path: string }) {
  const canEdit = useCanEdit();
  const qc = useQueryClient();
  const isCollection = !path.includes("/");
  const key = ["settings", path];
  const q = useQuery({ queryKey: key, queryFn: () => (isCollection ? api.collection(path) : api.folder(path)) });
  const [draft, setDraft] = useState<CollectionSettings | null>(null);
  const [tab, setTab] = useState<Tab>("overview");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setDraft(null), [path]);
  // Ctrl+S / ⌘+S saves, as on requests (the latest save is kept in a ref).
  const saveRef = useRef<(() => void) | null>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        saveRef.current?.();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  if (q.isPending)
    return (
      <div className="page">
        <section className="card settings-page">
          <Skeleton active paragraph={{ rows: 4 }} />
        </section>
      </div>
    );
  if (q.error)
    return (
      <div className="page">
        <section className="card settings-page">
          <Alert type="error" showIcon title={q.error.message} />
        </section>
      </div>
    );
  const c = draft ?? q.data;
  const dirty = draft !== null && JSON.stringify(draft) !== JSON.stringify(q.data);
  const edit = (patch: Partial<CollectionSettings>) => setDraft({ ...c, ...patch });

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const clean = { ...c, variables: c.variables?.filter((v) => v.key.trim()) };
      const saved = isCollection ? await api.saveCollection(path, clean) : await api.saveFolder(path, clean);
      qc.setQueryData(key, saved);
      setDraft(null);
      qc.invalidateQueries({ queryKey: ["workspace"] });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  saveRef.current = dirty && canEdit && !saving ? save : null;

  const pre = preList(c);
  const post = postList(c);
  const tabs: { id: Tab; label: string; count?: number }[] = [
    { id: "overview", label: "Overview" },
    { id: "variables", label: "Variables", count: c.variables?.length },
    { id: "auth", label: "Auth", count: c.auth ? 1 : 0 },
    { id: "pre", label: "Pre-processors", count: pre.length + (c.think ? 1 : 0) },
    { id: "post", label: "Post-processors", count: post.length },
  ];
  if (isCollection) tabs.push({ id: "mock", label: "Mock server" });

  return (
    <VarScope path={`${path}/`}>
      <div className="page">
        <section className="card settings-page">
          <header className="page-head">
            <div>
              <Input
                variant="borderless"
                className="title-input"
                value={c.name}
                onChange={(e) => edit({ name: e.target.value })}
                aria-label={isCollection ? "Collection name" : "Folder name"}
              />
              <p className="muted small">
                <code>
                  collections/{path}/{isCollection ? "collection.yaml" : "folder.yaml"}
                </code>
                {dirty && <span className="dirty"> · unsaved</span>} · {isCollection ? "every request in this collection" : "every request in this folder"} inherits
                these settings
              </p>
            </div>
            <div className="page-actions">
              {canEdit ? (
                <Button type="primary" disabled={!dirty || saving} loading={saving} icon={dirty ? <Save size={14} /> : <Check size={14} />} onClick={save} title="Save (Ctrl+S)">
                  {saving ? "Saving" : dirty ? "Save" : "Saved"}
                </Button>
              ) : (
                <ReadOnlyBadge />
              )}
            </div>
          </header>
          {error && <Alert type="error" showIcon title={error} />}
          <Tabs
            className="settings-tabs"
            activeKey={tab}
            onChange={(k) => setTab(k as Tab)}
            items={tabs.map((t) => ({
              key: t.id,
              label: (
                <>
                  {t.label}
                  {(t.count ?? 0) > 0 && <span className="count">{t.count}</span>}
                </>
              ),
            }))}
          />
          <div className="tab-panel settings-panel" role="tabpanel">
            {tab === "overview" && (
              <label className="paste">
                <span className="field-label">Description (Markdown, shown in generated docs)</span>
                <Input.TextArea className="code-input" value={c.description ?? ""} onChange={(e) => edit({ description: e.target.value || undefined })} rows={10} />
              </label>
            )}
            {tab === "variables" && (
              <VarsTable slug={path.split("/")[0]} vars={c.variables ?? []} onChange={(variables) => edit({ variables: variables.length ? variables : undefined })} />
            )}
            {tab === "auth" && (
              <>
                {isCollection && <p className="muted small">"Inherit" at the collection level means no auth.</p>}
                <AuthEditor auth={c.auth} onChange={(auth) => edit({ auth })} />
              </>
            )}
            {tab === "pre" && (
              <div className="proc-tab">
                <section>
                  <h3 className="sub-head">Default think time</h3>
                  <p className="muted small">
                    For every request {isCollection ? "in this collection" : "in this folder"} that doesn't set its own; a nearer folder's wins. Load tests and opted-in collection runs only.
                  </p>
                  <TimerEditor value={c.think} onChange={(think) => edit({ think })} inheritLabel={isCollection ? "No default" : "Inherit"} target="each request" />
                </section>
                <section>
                  <h3 className="sub-head">Pre-processors</h3>
                  <ProcessorList phase="pre" items={pre} onChange={(items) => edit(setPre(c, items))} />
                </section>
              </div>
            )}
            {tab === "post" && (
              <div className="proc-tab">
                <section>
                  <h3 className="sub-head">Post-processors</h3>
                  <ProcessorList phase="post" items={post} onChange={(items) => edit(setPost(c, items))} />
                </section>
              </div>
            )}
            {tab === "mock" && <MockPanel slug={path} />}
          </div>
        </section>
      </div>
    </VarScope>
  );
}

function VarsTable({ slug, vars, onChange }: { slug: string; vars: Variable[]; onChange: (v: Variable[]) => void }) {
  const [shown, setShown] = useState<Set<number>>(new Set());
  // Collection and folder variables share the collection's session scope.
  const current = useCurrentValues("collection", slug);
  const update = (i: number, patch: Partial<Variable>) => onChange(vars.map((v, j) => (j === i ? { ...v, ...patch } : v)));
  return (
    <>
      <div className="scroll-x">
        <table className="kv-edit vars-table settings-vars">
          <thead>
            <tr>
              <th className="kv-check">
                <span className="sr-only">Enabled</span>
              </th>
              <th>Variable</th>
              <th>
                Initial value <span className="th-note">saved in the file</span>
              </th>
              <th>
                Current value <span className="th-note">this session only</span>
              </th>
              <th className="kv-actions">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {vars.length === 0 && (
              <tr>
                <td colSpan={5} className="vars-empty muted">
                  No variables yet.
                </td>
              </tr>
            )}
            {vars.map((v, i) => {
              const masked = v.secret && !shown.has(i);
              const { has, value: cur, byRequest } = current.get(v.key);
              const differs = has && cur !== v.value;
              return (
                <tr key={i} className={v.disabled ? "is-off" : ""}>
                  <td className="kv-check">
                    <Checkbox checked={!v.disabled} onChange={(e) => update(i, { disabled: !e.target.checked || undefined })} aria-label={`Enable ${v.key || "variable"}`} />
                  </td>
                  <td>
                    <Input variant="borderless" className="mono" value={v.key} onChange={(e) => update(i, { key: e.target.value })} placeholder="name" aria-label="Variable name" spellCheck={false} />
                  </td>
                  <td>
                    <Input
                      variant="borderless"
                      className="mono"
                      type={masked ? "password" : "text"}
                      value={v.value}
                      onChange={(e) => update(i, { value: e.target.value })}
                      aria-label={`Initial value of ${v.key}`}
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </td>
                  <td>
                    <div className="current-cell">
                      <CurrentInput
                        value={cur === undefined ? v.value : (cur ?? "")}
                        placeholder={cur === null ? "unset this session" : ""}
                        masked={!!masked}
                        changed={differs}
                        disabled={!v.key}
                        label={`Current value of ${v.key}`}
                        onCommit={(val) => (val === v.value && !has ? undefined : current.set(v.key, val))}
                      />
                      {byRequest && differs && (
                        <Tooltip title="A request set this value (an extractor, a set-variables processor or a script)">
                          <span className="current-src">from a request</span>
                        </Tooltip>
                      )}
                      {differs && (
                        <>
                          <Tooltip title="Use as initial value (then save)">
                            <Button type="text" icon={<Upload size={13} />} aria-label={`Use current value of ${v.key} as initial`} onClick={() => update(i, { value: cur ?? "" })} />
                          </Tooltip>
                          <Tooltip title="Reset to initial value">
                            <Button type="text" icon={<RotateCcw size={13} />} aria-label={`Reset ${v.key}`} onClick={() => current.reset(v.key)} />
                          </Tooltip>
                        </>
                      )}
                    </div>
                  </td>
                  <td className="kv-actions">
                    <Tooltip title={v.secret ? "Secret (double-click to unmark)" : "Mark as secret"}>
                      <Button
                        type="text"
                        className={v.secret ? "is-on" : undefined}
                        icon={masked ? <EyeOff size={13} /> : <Eye size={13} />}
                        aria-label={v.secret ? `Show or hide ${v.key}` : `Mark ${v.key} as secret`}
                        onClick={() => {
                          if (!v.secret) return update(i, { secret: true });
                          const next = new Set(shown);
                          if (next.has(i)) next.delete(i);
                          else next.add(i);
                          setShown(next);
                        }}
                        onDoubleClick={() => v.secret && update(i, { secret: undefined })}
                      />
                    </Tooltip>
                    <Tooltip title="Remove">
                      <Button type="text" icon={<Trash2 size={13} />} aria-label={`Remove ${v.key}`} onClick={() => onChange(vars.filter((_, j) => j !== i))} />
                    </Tooltip>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <Button className="add-var" type="dashed" icon={<Plus size={13} />} onClick={() => onChange([...vars, { key: "", value: "" }])}>
        Add variable
      </Button>
    </>
  );
}
