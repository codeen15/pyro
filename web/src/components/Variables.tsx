import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App as AntApp, Button, Checkbox, Input, Menu, Skeleton, Space, Tag, Tooltip } from "antd";
import { Check, Download, Eye, EyeOff, Globe, Layers, Plus, RotateCcw, Save, Trash2, Upload } from "../icons";
import { api, saveFile, type Environment, type Variable } from "../api";
import { useApp } from "../store";

// Globals and environments, Postman-style: the initial value is saved in the
// workspace file (and shared through git); the current value is what this
// session uses, set by you or by scripts and extractors, and never written.

export function VariablesPage({ target }: { target: string }) {
  const select = useApp((s) => s.select);
  const envs = useQuery({ queryKey: ["environments"], queryFn: api.environments });
  const qc = useQueryClient();
  const [newName, setNewName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const create = async () => {
    const name = newName.trim();
    if (!name) return;
    setError(null);
    if (envs.data?.includes(name)) {
      setError(`An environment named ${name} already exists.`);
      return;
    }
    try {
      await api.saveEnvironment(name, { name, variables: [] });
      await qc.invalidateQueries({ queryKey: ["environments"] });
      setNewName("");
      select(`vars:env:${name}`);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const envName = target.startsWith("env:") ? target.slice(4) : null;
  return (
    <div className="page vars-page">
      <nav className="card vars-list" aria-label="Variable sets">
        <Menu
          className="vars-menu"
          mode="inline"
          selectedKeys={[target === "globals" ? "globals" : `env:${envName}`]}
          onClick={({ key }) => select(`vars:${key}`)}
          items={[
            { key: "globals", icon: <Globe size={14} />, label: "Globals" },
            {
              type: "group",
              key: "envs",
              label: "Environments",
              children: (envs.data ?? []).map((n) => ({ key: `env:${n}`, icon: <Layers size={14} />, label: n })),
            },
          ]}
        />
        {envs.data?.length === 0 && <p className="muted small vars-none">None yet.</p>}
        <form
          className="vars-new"
          onSubmit={(e) => {
            e.preventDefault();
            create();
          }}
        >
          <Space.Compact block>
            <Input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="New environment" aria-label="New environment name" />
            <Button htmlType="submit" icon={<Plus size={14} />} aria-label="Create environment" disabled={!newName.trim()} />
          </Space.Compact>
        </form>
        {error && <Alert className="vars-error" type="error" showIcon title={error} />}
      </nav>
      {target === "globals" ? <VariableEditor key="globals" scope="globals" /> : envName ? <VariableEditor key={envName} scope="environment" name={envName} /> : null}
    </div>
  );
}

function VariableEditor({ scope, name }: { scope: "globals" | "environment"; name?: string }) {
  const qc = useQueryClient();
  const { modal } = AntApp.useApp();
  const select = useApp((s) => s.select);
  const active = useApp((s) => s.environment);
  const setActive = useApp((s) => s.setEnvironment);
  const fileKey = scope === "globals" ? ["globals"] : ["environment", name];
  const file = useQuery({ queryKey: fileKey, queryFn: () => (scope === "globals" ? api.globals() : api.environment(name!)) });
  const session = useQuery({ queryKey: ["session-vars"], queryFn: api.sessionVariables, refetchOnWindowFocus: true });
  const [rows, setRows] = useState<Variable[] | null>(null);
  const [title, setTitle] = useState(name ?? "");
  const [revealed, setRevealed] = useState<Set<number>>(new Set());
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (file.data && rows === null) setRows(file.data.variables ?? []);
  }, [file.data, rows]);

  const current: Record<string, string | null> = (scope === "globals" ? session.data?.globals : session.data?.environments?.[name!]) ?? {};
  const list = rows ?? [];
  const saved = file.data?.variables ?? [];
  const dirty = rows !== null && (JSON.stringify(rows) !== JSON.stringify(saved) || (scope === "environment" && title.trim() !== name));

  const save = useMutation({
    mutationFn: async (vars: Variable[]) => {
      const env: Environment = { name: scope === "globals" ? "globals" : title.trim(), variables: vars.filter((v) => v.key.trim() !== "") };
      if (scope === "globals") return api.saveGlobals(env);
      const newName = title.trim();
      if (newName !== name) {
        await api.saveEnvironment(newName, env);
        await api.deleteEnvironment(name!);
        if (active === name) setActive(newName);
        return env;
      }
      return api.saveEnvironment(name!, env);
    },
    onSuccess: async (env) => {
      setError(null);
      setRows(env.variables ?? []);
      qc.setQueryData(fileKey, env);
      await qc.invalidateQueries({ queryKey: ["environments"] });
      if (scope === "environment" && title.trim() !== name) select(`vars:env:${title.trim()}`);
    },
    onError: (e) => setError((e as Error).message),
  });

  const setCurrent = async (key: string, value: string | null, reset = false) => {
    await api.setCurrentValue({ scope, name, key, value, reset });
    await qc.invalidateQueries({ queryKey: ["session-vars"] });
  };

  const update = (i: number, patch: Partial<Variable>) => setRows(list.map((v, j) => (j === i ? { ...v, ...patch } : v)));

  const deleteEnv = () =>
    modal.confirm({
      title: `Delete the ${name} environment?`,
      content: "Its file is removed from the workspace.",
      okText: "Delete",
      okButtonProps: { danger: true },
      onOk: async () => {
        await api.deleteEnvironment(name!);
        if (active === name) setActive(null);
        await qc.invalidateQueries({ queryKey: ["environments"] });
        select("vars:globals");
      },
    });

  if (file.isPending)
    return (
      <section className="card vars-editor">
        <Skeleton active paragraph={{ rows: 4 }} />
      </section>
    );
  if (file.error)
    return (
      <section className="card vars-editor">
        <Alert type="error" showIcon title={file.error.message} />
      </section>
    );

  return (
    <section className="card vars-editor">
      <header className="page-head">
        <div>
          {scope === "globals" ? (
            <h1>Globals</h1>
          ) : (
            <Input variant="borderless" className="title-input" value={title} onChange={(e) => setTitle(e.target.value)} aria-label="Environment name" />
          )}
          <p className="muted small">
            <code>{scope === "globals" ? "globals.yaml" : `environments/${name}.env.yaml`}</code>
            {dirty && <span className="dirty"> · unsaved</span>}
            {scope === "globals" ? " · available in every request, under environment and collection variables" : ""}
          </p>
        </div>
        <div className="page-actions">
          {scope === "environment" &&
            (active === name ? (
              <Tag color="success" icon={<Check size={12} />} className="in-use">
                In use
              </Tag>
            ) : (
              <Button onClick={() => setActive(name!)}>Use this environment</Button>
            ))}
          {scope === "environment" && (
            <Tooltip title="Export to Postman">
              <Button
                icon={<Download size={14} />}
                aria-label="Export to Postman"
                onClick={async () => {
                  const ex = await api.exportEnvironment(name!);
                  saveFile(ex.filename, JSON.stringify(ex.content, null, "\t"));
                }}
              />
            </Tooltip>
          )}
          {scope === "environment" && (
            <Tooltip title="Delete environment">
              <Button danger icon={<Trash2 size={14} />} aria-label="Delete environment" onClick={deleteEnv} />
            </Tooltip>
          )}
          <Button type="primary" disabled={!dirty || save.isPending} loading={save.isPending} icon={dirty ? <Save size={14} /> : <Check size={14} />} onClick={() => save.mutate(list)}>
            {save.isPending ? "Saving" : dirty ? "Save" : "Saved"}
          </Button>
        </div>
      </header>
      {error && <Alert type="error" showIcon title={error} />}

      <div className="scroll-x">
        <table className="kv-edit vars-table">
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
            {list.length === 0 && (
              <tr>
                <td colSpan={5} className="vars-empty muted">
                  No variables yet.
                </td>
              </tr>
            )}
            {list.map((v, i) => {
              const has = Object.prototype.hasOwnProperty.call(current, v.key) && v.key !== "";
              const cur = has ? current[v.key] : undefined;
              const differs = has && cur !== v.value;
              const masked = v.secret && !revealed.has(i);
              return (
                <tr key={i} className={v.disabled ? "is-off" : ""}>
                  <td className="kv-check">
                    <Checkbox checked={!v.disabled} onChange={(e) => update(i, { disabled: !e.target.checked })} aria-label={`Enable ${v.key || "variable"}`} />
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
                        onCommit={(val) => (val === v.value && !has ? undefined : setCurrent(v.key, val))}
                      />
                      {differs && (
                        <>
                          <Tooltip title="Save as initial value">
                            <Button
                              type="text"
                              size="small"
                              icon={<Upload size={13} />}
                              aria-label={`Save current value of ${v.key} as initial`}
                              onClick={async () => {
                                const next = list.map((x, j) => (j === i ? { ...x, value: cur ?? "" } : x));
                                setRows(next);
                                await save.mutateAsync(next);
                                await setCurrent(v.key, null, true);
                              }}
                            />
                          </Tooltip>
                          <Tooltip title="Reset to initial value">
                            <Button type="text" size="small" icon={<RotateCcw size={13} />} aria-label={`Reset ${v.key}`} onClick={() => setCurrent(v.key, null, true)} />
                          </Tooltip>
                        </>
                      )}
                    </div>
                  </td>
                  <td className="kv-actions">
                    <Tooltip title={v.secret ? (masked ? "Show (double-click to unmark)" : "Hide (double-click to unmark)") : "Mark as secret"}>
                      <Button
                        type="text"
                        size="small"
                        className={v.secret ? "is-on" : undefined}
                        icon={masked ? <EyeOff size={13} /> : <Eye size={13} />}
                        aria-label={v.secret ? (masked ? `Show ${v.key}` : `Hide ${v.key}`) : `Mark ${v.key} as secret`}
                        onClick={() => {
                          if (!v.secret) {
                            update(i, { secret: true });
                            return;
                          }
                          const next = new Set(revealed);
                          if (next.has(i)) next.delete(i);
                          else next.add(i);
                          setRevealed(next);
                        }}
                        onDoubleClick={() => v.secret && update(i, { secret: false })}
                      />
                    </Tooltip>
                    <Tooltip title="Remove">
                      <Button type="text" size="small" icon={<Trash2 size={13} />} aria-label={`Remove ${v.key}`} onClick={() => setRows(list.filter((_, j) => j !== i))} />
                    </Tooltip>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <Button className="add-var" type="dashed" icon={<Plus size={13} />} onClick={() => setRows([...list, { key: "", value: "" }])}>
        Add variable
      </Button>
      <p className="muted small vars-help">
        Use variables as <code>{"{{name}}"}</code> in URLs, headers, bodies and auth. Secret values are masked here; they are still saved in the file, so keep
        real credentials out of shared workspaces or put them only in current values.
      </p>
    </section>
  );
}

/** A current-value input that commits on blur or Enter. */
function CurrentInput(props: { value: string; placeholder: string; masked: boolean; changed: boolean; disabled: boolean; label: string; onCommit: (v: string) => void }) {
  const [text, setText] = useState(props.value);
  useEffect(() => setText(props.value), [props.value]);
  const commit = () => text !== props.value && props.onCommit(text);
  return (
    <Input
      variant="borderless"
      className={`mono ${props.changed ? "is-changed" : ""}`}
      type={props.masked ? "password" : "text"}
      value={text}
      placeholder={props.placeholder}
      disabled={props.disabled}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()}
      aria-label={props.label}
      autoComplete="off"
      spellCheck={false}
    />
  );
}
