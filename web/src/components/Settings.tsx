import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, Plus, Save, Trash2 } from "lucide-react";
import { api, type CollectionSettings, type Variable } from "../api";
import { AuthEditor } from "./RequestEditor";
import { ScriptsEditor } from "./TestsEditor";
import { MockPanel } from "./Mock";

// Settings a collection or folder passes down to its requests: variables,
// auth and scripts (collection.yaml / folder.yaml).

type Tab = "overview" | "variables" | "auth" | "scripts" | "mock";

export function SettingsPage({ path }: { path: string }) {
  const qc = useQueryClient();
  const isCollection = !path.includes("/");
  const key = ["settings", path];
  const q = useQuery({ queryKey: key, queryFn: () => (isCollection ? api.collection(path) : api.folder(path)) });
  const [draft, setDraft] = useState<CollectionSettings | null>(null);
  const [tab, setTab] = useState<Tab>("overview");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setDraft(null), [path]);

  if (q.isPending) return <div className="pad muted">Loading…</div>;
  if (q.error) return <div className="pad inline-error">{q.error.message}</div>;
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

  const tabs: { id: Tab; label: string; count?: number }[] = [
    { id: "overview", label: "Overview" },
    { id: "variables", label: "Variables", count: c.variables?.length },
    { id: "auth", label: "Auth", count: c.auth ? 1 : 0 },
    { id: "scripts", label: "Scripts", count: (c.scripts?.pre?.trim() ? 1 : 0) + (c.scripts?.post?.trim() ? 1 : 0) },
  ];
  if (isCollection) tabs.push({ id: "mock", label: "Mock server" });

  return (
    <section className="loadtest settings-page">
      <header className="lt-head">
        <div>
          <input className="title-input" value={c.name} onChange={(e) => edit({ name: e.target.value })} aria-label={isCollection ? "Collection name" : "Folder name"} />
          <p className="muted small">
            <code>collections/{path}/{isCollection ? "collection.yaml" : "folder.yaml"}</code>
            {dirty && <span className="dirty"> · unsaved</span>} · {isCollection ? "every request in this collection" : "every request in this folder"} inherits these settings
          </p>
        </div>
        <div className="lt-actions">
          <button className="btn primary" disabled={!dirty || saving} onClick={save}>
            <Save size={14} /> {saving ? "Saving…" : dirty ? "Save" : "Saved"}
          </button>
        </div>
      </header>
      {error && <p className="inline-error">{error}</p>}
      <div className="tabs" role="tablist">
        {tabs.map((t) => (
          <button key={t.id} role="tab" aria-selected={tab === t.id} className="tab" onClick={() => setTab(t.id)}>
            {t.label}
            {(t.count ?? 0) > 0 && <span className="count">{t.count}</span>}
          </button>
        ))}
      </div>
      <div className="tab-panel">
        {tab === "overview" && (
          <label className="paste">
            <span className="field-label">Description (Markdown, shown in generated docs)</span>
            <textarea className="code-input" value={c.description ?? ""} onChange={(e) => edit({ description: e.target.value || undefined })} rows={10} />
          </label>
        )}
        {tab === "variables" && <VarsTable vars={c.variables ?? []} onChange={(variables) => edit({ variables: variables.length ? variables : undefined })} />}
        {tab === "auth" && (
          <>
            {isCollection && <p className="muted small">"Inherit" at the collection level means no auth.</p>}
            <AuthEditor auth={c.auth} onChange={(auth) => edit({ auth })} />
          </>
        )}
        {tab === "scripts" && (
          <>
            <p className="muted small">Run before (pre-request) and after (tests) every request below, outermost first.</p>
            <ScriptsEditor scripts={c.scripts} onChange={(scripts) => edit({ scripts })} />
          </>
        )}
        {tab === "mock" && <MockPanel slug={path} />}
      </div>
    </section>
  );
}

function VarsTable({ vars, onChange }: { vars: Variable[]; onChange: (v: Variable[]) => void }) {
  const [shown, setShown] = useState<Set<number>>(new Set());
  const update = (i: number, patch: Partial<Variable>) => onChange(vars.map((v, j) => (j === i ? { ...v, ...patch } : v)));
  return (
    <>
      <div className="scroll-x">
        <table className="data-table vars-table">
          <thead>
            <tr>
              <th className="pick-col">
                <span className="sr-only">Enabled</span>
              </th>
              <th>Variable</th>
              <th>Value</th>
              <th className="pick-col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {vars.map((v, i) => {
              const masked = v.secret && !shown.has(i);
              return (
                <tr key={i} className={v.disabled ? "is-disabled" : ""}>
                  <td className="pick-col">
                    <input type="checkbox" checked={!v.disabled} onChange={(e) => update(i, { disabled: !e.target.checked || undefined })} aria-label={`Enable ${v.key || "variable"}`} />
                  </td>
                  <td>
                    <input className="cell mono" value={v.key} onChange={(e) => update(i, { key: e.target.value })} placeholder="name" aria-label="Variable name" />
                  </td>
                  <td>
                    <input className="cell mono" type={masked ? "password" : "text"} value={v.value} onChange={(e) => update(i, { value: e.target.value })} aria-label={`Value of ${v.key}`} autoComplete="off" />
                  </td>
                  <td className="pick-col row-tools">
                    <button
                      className="icon-btn"
                      title={v.secret ? "Secret (double-click to unmark)" : "Mark as secret"}
                      aria-label={v.secret ? `Show or hide ${v.key}` : `Mark ${v.key} as secret`}
                      onClick={() => {
                        if (!v.secret) return update(i, { secret: true });
                        const next = new Set(shown);
                        if (next.has(i)) next.delete(i);
                        else next.add(i);
                        setShown(next);
                      }}
                      onDoubleClick={() => v.secret && update(i, { secret: undefined })}
                    >
                      {masked ? <EyeOff size={13} /> : <Eye size={13} />}
                    </button>
                    <button className="icon-btn" aria-label={`Remove ${v.key}`} onClick={() => onChange(vars.filter((_, j) => j !== i))}>
                      <Trash2 size={13} />
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <button className="btn ghost small add-var" onClick={() => onChange([...vars, { key: "", value: "" }])}>
        <Plus size={13} /> Add variable
      </button>
    </>
  );
}
