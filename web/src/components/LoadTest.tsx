import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  AutoComplete,
  Button,
  Checkbox,
  Empty,
  Input,
  InputNumber,
  Radio,
  Select,
  Table,
  Tabs,
} from "antd";
import {
  ArrowDown,
  ArrowUp,
  GitCompareArrows,
  Play,
  Plus,
  ShieldAlert,
  Trash2,
  X,
} from "../icons";
import YAML, { isMap, isSeq, type Document, type YAMLMap } from "yaml";
import { api, ApiError, type TreeNode } from "../api";
import { formatMs, heatOf } from "../heat";
import {
  generatePlan,
  PROFILES,
  slugify,
  type ProfileId,
} from "../loadProfiles";
import { useApp } from "../store";
import { LineChart, clock } from "./LineChart";
import { DataEditor, StreamStepEditor } from "./LoadData";
import { SaveButton } from "./RequestEditor";
import { LoadTestMenu } from "./LoadActions";

const EXECUTORS = [
  { id: "constant-vus", label: "Constant VUs" },
  { id: "ramping-vus", label: "Ramping VUs" },
  { id: "per-vu-iterations", label: "Iterations per VU" },
  { id: "constant-arrival-rate", label: "Constant arrival rate" },
  { id: "ramping-arrival-rate", label: "Ramping arrival rate" },
];

/** Flattens the workspace tree into request paths. */
export function useRequestPaths() {
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  return useMemo(() => {
    const out: { path: string; name: string; kind?: string; method?: string }[] = [];
    const walk = (n: TreeNode) =>
      n.type === "request"
        ? out.push({ path: n.path, name: n.name, kind: n.kind, method: n.method })
        : n.children?.forEach(walk);
    ws.data?.collections.forEach(walk);
    return out;
  }, [ws.data]);
}

type Tab = "designer" | "yaml" | "runs";

/** Counts shown in the stat tiles; null when the YAML doesn't parse. */
function planCounts(text: string) {
  const doc = YAML.parseDocument(text);
  if (doc.errors.length > 0) return null;
  const scenarios = doc.get("scenarios");
  const thresholds = doc.get("thresholds");
  let steps = 0;
  if (isMap(scenarios))
    for (const p of scenarios.items) {
      const flow = isMap(p.value) ? p.value.get("flow") : undefined;
      if (isSeq(flow)) steps += flow.items.length;
    }
  let conds = 0;
  if (isMap(thresholds))
    for (const p of thresholds.items)
      if (isSeq(p.value)) conds += p.value.items.length;
  return {
    scenarios: isMap(scenarios) ? scenarios.items.length : 0,
    steps,
    thresholds: conds,
  };
}

export function LoadTestPage({ name }: { name: string }) {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const environment = useApp((s) => s.environment);
  const q = useQuery({
    queryKey: ["loadtest", name],
    queryFn: () => api.loadTest(name),
    retry: false,
  });
  const [text, setText] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("designer");
  const [confirm, setConfirm] = useState<string[] | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: api.agents,
    staleTime: 10_000,
  });
  const [where, setWhere] = useState<"local" | "agents">("local");
  const agentCount = agents.data?.length ?? 0;
  const distributed = where === "agents" && agentCount > 0;
  const yamlText = text ?? q.data?.yaml ?? "";
  const dirty = text !== null && text !== q.data?.yaml;
  const counts = useMemo(() => planCounts(yamlText), [yamlText]);

  const save = useMutation({
    mutationFn: () => api.saveLoadTest(name, yamlText),
    onSuccess: (v) => {
      qc.setQueryData(["loadtest", name], v);
      setText(null);
      qc.invalidateQueries({ queryKey: ["loadtests"] });
    },
  });

  const start = async (confirmHosts = false) => {
    setStartError(null);
    try {
      if (dirty) await save.mutateAsync();
      const r = await api.startLoadTest(name, {
        environment: environment ?? undefined,
        confirmHosts,
        distributed,
      });
      setConfirm(null);
      qc.invalidateQueries({ queryKey: ["loadruns"] });
      select(`loadrun:${r.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.body?.needsConfirmation) {
        setConfirm((e.body.hosts as string[]) ?? []);
      } else if (e instanceof ApiError && e.body?.running) {
        setStartError(
          "Another load test is running. Open it from the Load tab, or stop it first.",
        );
      } else {
        setStartError((e as Error).message);
      }
    }
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (dirty) save.mutate();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (q.isPending) return <div className="pad muted">Loading…</div>;
  if (q.error)
    return (
      <div className="page">
        <Alert type="error" showIcon title={q.error.message} />
      </div>
    );
  const v = q.data;

  return (
    <div className="page lt-page">
      <section className="card lt-header">
        <header className="lt-top">
          <div className="lt-title">
            <h1>{v.title || name}</h1>
            <p className="lt-target">
              <span className="lt-target-label">Target</span>
              <span className="mono">
                {environment ? environment : "no environment"} · loadtests/
                {name}.load.yaml
              </span>
              {dirty && <span className="dirty">unsaved</span>}
            </p>
            <p className="muted small lt-note">
              Environment from the top bar. Only load test systems you own or
              are allowed to test.
            </p>
          </div>
          <div className="lt-head-actions">
            <SaveButton
              dirty={dirty}
              saving={save.isPending}
              onSave={() => save.mutate()}
            />
            <LoadTestMenu name={name} title={v.title || name} />
            {agentCount > 0 && (
              <Select
                className="run-on"
                value={where}
                onChange={(w) => {
                  setWhere(w);
                  agents.refetch();
                }}
                aria-label="Run on"
                popupMatchSelectWidth={false}
                options={[
                  { value: "local", label: "This machine" },
                  {
                    value: "agents",
                    label: `${agentCount} agent${agentCount === 1 ? "" : "s"}`,
                  },
                ]}
              />
            )}
            <Button
              type="primary"
              className="lt-run"
              icon={<Play size={16} />}
              onClick={() => start(false)}
              disabled={!!v.error && !dirty}
            >
              Start
            </Button>
          </div>
        </header>
        {distributed && agents.data && (
          <ul className="agent-list small" aria-label="Agents">
            {agents.data.map((a) => (
              <li key={a.address}>
                <span className="mono">{a.info?.name ?? a.address}</span>
                {a.info ? (
                  <span className="muted">
                    {a.address} · {a.info.cpus} CPUs ·{" "}
                    {a.info.busy ? "busy with another test" : "ready"}
                  </span>
                ) : (
                  <span className="fail-text">{a.error}</span>
                )}
              </li>
            ))}
            <li className="muted">
              The plan's VUs and arrival rates are split evenly between them;
              results are merged here.
            </li>
          </ul>
        )}
        {save.error && (
          <Alert type="error" showIcon title={save.error.message} />
        )}
        {!dirty && v.error && (
          <Alert
            type="error"
            showIcon
            title="The plan isn't valid"
            description={<pre className="mono pre-wrap lt-pre">{v.error}</pre>}
          />
        )}
        {startError && <Alert type="error" showIcon title={startError} />}
        {confirm && (
          <Alert
            type="warning"
            showIcon
            icon={<ShieldAlert size={18} />}
            title={
              <>
                <strong>This test will send traffic to:</strong>{" "}
                {confirm.join(", ")}
              </>
            }
            description={
              <>
                <p className="lt-confirm-text">
                  These hosts aren't on this machine or in{" "}
                  <code>loadAllowHosts</code> in pyro.yaml. Make sure you're
                  allowed to load test them.
                </p>
                <div className="lt-confirm-actions">
                  <Button type="primary" onClick={() => start(true)}>
                    Send traffic to{" "}
                    {confirm.length === 1 ? "this host" : "these hosts"}
                  </Button>
                  <Button onClick={() => setConfirm(null)}>Cancel</Button>
                </div>
              </>
            }
          />
        )}
      </section>

      <div className="lt-stats">
        <StatTile label="Peak VUs" value={v.peakVUs ? v.peakVUs : "–"} />
        <StatTile label="Scenarios" value={counts?.scenarios ?? "–"} />
        <StatTile label="Flow steps" value={counts?.steps ?? "–"} />
        <StatTile label="Thresholds" value={counts?.thresholds ?? "–"} />
      </div>

      <section className="card lt-body">
        <Tabs
          className="lt-tabs"
          activeKey={tab}
          onChange={(k) => setTab(k as Tab)}
          items={(["designer", "yaml", "runs"] as Tab[]).map((t) => ({
            key: t,
            label: t === "yaml" ? "YAML" : t[0].toUpperCase() + t.slice(1),
          }))}
        />
        <div className="tab-panel" role="tabpanel">
          {tab === "designer" && (
            <Designer text={yamlText} onChange={setText} />
          )}
          {tab === "yaml" && (
            <Input.TextArea
              className="code-input mono yaml-editor"
              value={yamlText}
              onChange={(e) => setText(e.target.value)}
              spellCheck={false}
              aria-label="Load test YAML"
            />
          )}
          {tab === "runs" && <RunsList test={name} />}
        </div>
      </section>
    </div>
  );
}

function StatTile({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <section className="card lt-stat">
      <span className="lt-stat-label">{label}</span>
      <span className="lt-stat-value">{value}</span>
    </section>
  );
}

// --- Designer: edits the YAML document in place, so comments survive --------

function Designer({
  text,
  onChange,
}: {
  text: string;
  onChange: (t: string) => void;
}) {
  const requests = useRequestPaths();
  const doc = useMemo(() => YAML.parseDocument(text), [text]);
  if (doc.errors.length > 0) {
    return (
      <Alert
        type="error"
        showIcon
        title={`The YAML doesn't parse (${doc.errors[0].message}). Fix it in the YAML tab.`}
      />
    );
  }
  const update = (fn: (d: Document) => void) => {
    const d = YAML.parseDocument(text);
    fn(d);
    onChange(String(d));
  };
  const scenarios = doc.get("scenarios");
  const names = isMap(scenarios)
    ? scenarios.items.map((p) =>
        String((p.key as { value?: unknown })?.value ?? p.key),
      )
    : [];

  return (
    <div className="designer">
      <div className="lt-fields">
        <label className="lt-field span-2">
          <span className="lt-label">Name</span>
          <Input
            value={String(doc.get("name") ?? "")}
            onChange={(e) => update((d) => d.set("name", e.target.value))}
          />
        </label>
        <label className="lt-field">
          <span className="lt-label">Max requests/s</span>
          <Input
            className="mono"
            inputMode="decimal"
            value={
              doc.get("maxRPS") === undefined ? "" : String(doc.get("maxRPS"))
            }
            placeholder="no cap"
            aria-label="Max requests per second"
            onChange={(e) =>
              update((d) => {
                const n = Number(e.target.value);
                if (
                  e.target.value.trim() === "" ||
                  !Number.isFinite(n) ||
                  n <= 0
                )
                  d.delete("maxRPS");
                else d.set("maxRPS", n);
              })
            }
          />
        </label>
        <p className="lt-hint lt-field-hint">
          Max requests/s spaces request starts across all VUs; waiting isn't
          counted as response time.
        </p>
      </div>

      {names.map((sn) => (
        <ScenarioEditor
          key={sn}
          name={sn}
          doc={doc}
          update={update}
          requests={requests}
        />
      ))}
      <div>
        <Button
          icon={<Plus size={13} />}
          onClick={() =>
            update((d) =>
              d.setIn(
                ["scenarios", `scenario-${names.length + 1}`],
                d.createNode({
                  executor: "constant-vus",
                  vus: 10,
                  duration: "1m",
                  flow: [{ request: requests[0]?.path ?? "" }],
                }),
              ),
            )
          }
        >
          Add scenario
        </Button>
      </div>

      <ThresholdsEditor doc={doc} update={update} />
    </div>
  );
}

const num = (v: unknown) =>
  v === undefined || v === null || v === "" ? "" : String(v);

function ScenarioEditor({
  name,
  doc,
  update,
  requests,
}: {
  name: string;
  doc: Document;
  update: (fn: (d: Document) => void) => void;
  requests: { path: string; name: string; kind?: string; method?: string }[];
}) {
  const base = ["scenarios", name];
  const get = (k: string) => doc.getIn([...base, k]);
  const set = (k: string, v: unknown) =>
    update((d) => {
      if (v === "" || v === undefined) d.deleteIn([...base, k]);
      else d.setIn([...base, k], v);
    });
  const executor = String(get("executor") ?? "");
  const field = (
    k: string,
    label: string,
    kind: "int" | "dur" | "num",
    placeholder = "",
  ) => (
    <label className="lt-field">
      <span className="lt-label">{label}</span>
      <Input
        className="mono"
        value={num(get(k))}
        placeholder={placeholder}
        onChange={(e) => {
          const raw = e.target.value.trim();
          set(
            k,
            kind === "dur" || raw === "" || Number.isNaN(Number(raw))
              ? raw
              : Number(raw),
          );
        }}
      />
    </label>
  );
  const stagesNode = get("stages");
  const stages = isSeq(stagesNode)
    ? stagesNode.items.map((it) => ({
        duration: String((it as YAMLMap).get("duration") ?? ""),
        target: Number((it as YAMLMap).get("target") ?? 0),
      }))
    : [];
  const ramping =
    executor === "ramping-vus" || executor === "ramping-arrival-rate";
  const arrival = executor.includes("arrival");

  const preview = useMemo(() => {
    if (!ramping) return null;
    const x = [0];
    const y = [
      Number(get(executor === "ramping-vus" ? "startVUs" : "startRate") ?? 0),
    ];
    let t = 0;
    for (const s of stages) {
      t += parseDur(s.duration);
      x.push(t);
      y.push(s.target);
    }
    return { x, y };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doc, ramping, executor]);

  return (
    <section className="scenario">
      <header className="sc-head">
        <Input
          className="sc-name mono"
          defaultValue={name}
          aria-label="Scenario name"
          onBlur={(e) => {
            const nn = e.target.value.trim();
            if (!nn || nn === name) return;
            update((d) => {
              const node = d.getIn(["scenarios", name]);
              d.deleteIn(["scenarios", name]);
              d.setIn(["scenarios", nn], node);
            });
          }}
        />
        <Select
          className="sc-executor"
          value={executor}
          onChange={(x) => set("executor", x)}
          aria-label="Executor"
          popupMatchSelectWidth={false}
          options={EXECUTORS.map((x) => ({ value: x.id, label: x.label }))}
        />
        <Button
          type="text"
          className="sc-remove"
          icon={<Trash2 size={14} />}
          onClick={() => update((d) => d.deleteIn(["scenarios", name]))}
          aria-label={`Remove scenario ${name}`}
          title="Remove scenario"
        />
      </header>

      <div className="lt-fields">
        {executor === "constant-vus" && (
          <>
            {field("vus", "VUs", "int")}
            {field("duration", "Duration", "dur", "1m")}
          </>
        )}
        {executor === "ramping-vus" &&
          field("startVUs", "Start VUs", "int", "0")}
        {executor === "per-vu-iterations" && (
          <>
            {field("vus", "VUs", "int")}
            {field("iterations", "Iterations each", "int")}
            {field("maxDuration", "Max duration", "dur", "10m")}
          </>
        )}
        {executor === "constant-arrival-rate" && (
          <>
            {field("rate", "Iterations", "num")}
            {field("timeUnit", "Per", "dur", "1s")}
            {field("duration", "Duration", "dur", "1m")}
          </>
        )}
        {executor === "ramping-arrival-rate" && (
          <>
            {field("startRate", "Start rate", "num", "0")}
            {field("timeUnit", "Per", "dur", "1s")}
          </>
        )}
        {arrival && (
          <>
            {field("preAllocatedVUs", "Pre-allocated VUs", "int", "10")}
            {field("maxVUs", "Max VUs", "int")}
          </>
        )}
        {field("gracefulStop", "Graceful stop", "dur", "30s")}
      </div>

      {ramping && (
        <div className="stages">
          <table className="lt-table">
            <thead>
              <tr>
                <th>Duration</th>
                <th>
                  {executor === "ramping-vus" ? "Target VUs" : "Target rate"}
                </th>
                <th className="lt-table-actions" aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {stages.map((s, i) => (
                <tr key={i}>
                  <td>
                    <Input
                      className="mono"
                      value={s.duration}
                      onChange={(e) =>
                        update((d) =>
                          d.setIn(
                            [...base, "stages", i, "duration"],
                            e.target.value,
                          ),
                        )
                      }
                      aria-label={`Stage ${i + 1} duration`}
                    />
                  </td>
                  <td>
                    <Input
                      className="mono"
                      value={String(s.target)}
                      onChange={(e) =>
                        update((d) =>
                          d.setIn(
                            [...base, "stages", i, "target"],
                            Number(e.target.value) || 0,
                          ),
                        )
                      }
                      aria-label={`Stage ${i + 1} target`}
                    />
                  </td>
                  <td className="lt-table-actions">
                    <Button
                      type="text"
                      icon={<X size={14} />}
                      onClick={() =>
                        update((d) => d.deleteIn([...base, "stages", i]))
                      }
                      aria-label={`Remove stage ${i + 1}`}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <div>
            <Button
              icon={<Plus size={13} />}
              onClick={() =>
                update((d) => {
                  const node = d.createNode({
                    duration: "1m",
                    target: stages[stages.length - 1]?.target ?? 10,
                  });
                  if (isSeq(d.getIn([...base, "stages"])))
                    d.addIn([...base, "stages"], node);
                  else
                    d.setIn(
                      [...base, "stages"],
                      d.createNode([{ duration: "1m", target: 10 }]),
                    );
                })
              }
            >
              Add stage
            </Button>
          </div>
          {preview && preview.x.length > 1 && (
            <LineChart
              title={
                executor === "ramping-vus"
                  ? "Target VUs over time"
                  : "Target rate over time"
              }
              x={preview.x}
              series={[
                {
                  name: "target",
                  color: "var(--viz-series)",
                  values: preview.y,
                },
              ]}
              unit=""
              height={130}
            />
          )}
        </div>
      )}

      <FlowEditor doc={doc} base={base} update={update} requests={requests} />
      <DataEditor doc={doc} base={base} update={update} />
    </section>
  );
}

function parseDur(s: string): number {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)?$/.exec(s.trim());
  if (!m) {
    let total = 0;
    for (const part of s.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h)/g))
      total += parseDur(part[0]);
    return total;
  }
  const v = Number(m[1]);
  return m[2] === "ms"
    ? v / 1000
    : m[2] === "m"
      ? v * 60
      : m[2] === "h"
        ? v * 3600
        : v;
}

function FlowEditor({
  doc,
  base,
  update,
  requests,
}: {
  doc: Document;
  base: string[];
  update: (fn: (d: Document) => void) => void;
  requests: { path: string; name: string; kind?: string; method?: string }[];
}) {
  const flow = doc.getIn([...base, "flow"]);
  const items = isSeq(flow) ? flow.items : [];
  const path = [...base, "flow"];
  const move = (i: number, dir: -1 | 1) =>
    update((d) => {
      const seq = d.getIn(path);
      if (!isSeq(seq)) return;
      const j = i + dir;
      if (j < 0 || j >= seq.items.length) return;
      [seq.items[i], seq.items[j]] = [seq.items[j], seq.items[i]];
    });
  return (
    <div className="flow">
      <h3 className="sub-head">Flow (each iteration runs these in order)</h3>
      <ol className="flow-list">
        {items.map((it, i) => {
          const m = it as YAMLMap;
          const request = m.get?.("request") as string | undefined;
          const think = m.get?.("think");
          const script = m.get?.("script") as string | undefined;
          const scriptFile = m.get?.("scriptFile") as string | undefined;
          let body: React.ReactNode;
          // Hold and send settings belong to connect requests; send, ping and
          // close steps carry their own messages and waits.
          const target = requests.find((r) => r.path === request);
          const streamKind = !target?.method || target.method === "connect" ? target?.kind : undefined;
          if (script !== undefined) {
            body = (
              <div className="script-step">
                <span className="step-kind">Script</span>
                <Input.TextArea
                  className="code-input mono"
                  value={script}
                  onChange={(e) =>
                    update((d) =>
                      d.setIn([...path, i, "script"], e.target.value),
                    )
                  }
                  spellCheck={false}
                  rows={Math.min(
                    14,
                    Math.max(4, script.split("\n").length + 1),
                  )}
                  aria-label="Script"
                />
                <span className="muted small">
                  <code>pyro.request(path)</code>,{" "}
                  <code>pyro.http.get/post(url)</code>,{" "}
                  <code>pyro.check</code>, <code>pyro.sleep</code>,{" "}
                  <code>pyro.group</code>, <code>pyro.metrics</code>,{" "}
                  <code>pyro.vars</code>; see docs/load-engine.md.
                </span>
              </div>
            );
          } else if (scriptFile !== undefined) {
            body = (
              <>
                <span className="step-kind">Script</span>
                <Input
                  className="mono step-file"
                  value={scriptFile}
                  onChange={(e) =>
                    update((d) =>
                      d.setIn([...path, i, "scriptFile"], e.target.value),
                    )
                  }
                  aria-label="Script file"
                />
                <span className="muted small">file in the workspace</span>
              </>
            );
          } else if (request !== undefined) {
            const known = requests.some((r) => r.path === request);
            body = (
              <>
                <span className="step-kind">Request</span>
                <Select
                  className="step-request mono"
                  value={request}
                  onChange={(r) =>
                    update((d) => d.setIn([...path, i, "request"], r))
                  }
                  aria-label="Request"
                  showSearch
                  popupMatchSelectWidth={false}
                  options={[
                    ...(known
                      ? []
                      : [
                          {
                            value: request,
                            label: `${request || "Choose a request"} (missing)`,
                          },
                        ]),
                    ...requests.map((r) => ({
                      value: r.path,
                      label: `${r.path}${r.kind && r.kind !== "http" ? ` (${r.kind})` : ""}`,
                    })),
                  ]}
                />
                <Checkbox
                  checked={!!m.get("once")}
                  onChange={(e) =>
                    update((d) =>
                      e.target.checked
                        ? d.setIn([...path, i, "once"], true)
                        : d.deleteIn([...path, i, "once"]),
                    )
                  }
                >
                  Once per VU
                </Checkbox>
              </>
            );
          } else if (think !== undefined) {
            const min = isMap(think)
              ? String(think.get("min") ?? "")
              : String(think);
            const max = isMap(think)
              ? String(think.get("max") ?? "")
              : String(think);
            body = (
              <>
                <span className="step-kind">Think</span>
                <Input
                  className="mono step-dur"
                  value={min}
                  onChange={(e) =>
                    update((d) =>
                      d.setIn(
                        [...path, i, "think"],
                        d.createNode({ min: e.target.value, max }),
                      ),
                    )
                  }
                  aria-label="Think min"
                />
                <span className="muted">to</span>
                <Input
                  className="mono step-dur"
                  value={max}
                  onChange={(e) =>
                    update((d) =>
                      d.setIn(
                        [...path, i, "think"],
                        d.createNode({ min, max: e.target.value }),
                      ),
                    )
                  }
                  aria-label="Think max"
                />
              </>
            );
          } else {
            const kind =
              ["group", "loop", "if", "weighted"].find(
                (k) => m.get?.(k) !== undefined,
              ) ?? "step";
            body = (
              <>
                <span className="step-kind">
                  {kind[0].toUpperCase() + kind.slice(1)}
                </span>
                <span className="muted small">
                  {kind === "group" ? String(m.get("group")) : ""} Edit this
                  step in the YAML tab.
                </span>
              </>
            );
          }
          return (
            <li key={i} className="flow-step" data-request={request}>
              <span className="step-no">{i + 1}</span>
              {body}
              <span className="step-actions">
                <Button
                  type="text"
                  icon={<ArrowUp size={13} />}
                  onClick={() => move(i, -1)}
                  aria-label="Move up"
                  disabled={i === 0}
                />
                <Button
                  type="text"
                  icon={<ArrowDown size={13} />}
                  onClick={() => move(i, 1)}
                  aria-label="Move down"
                  disabled={i === items.length - 1}
                />
                <Button
                  type="text"
                  icon={<X size={13} />}
                  onClick={() => update((d) => d.deleteIn([...path, i]))}
                  aria-label="Remove step"
                />
              </span>
              {request !== undefined &&
                (streamKind === "websocket" || streamKind === "socketio") && (
                  <StreamStepEditor
                    doc={doc}
                    path={[...path, i]}
                    update={update}
                    request={request}
                    kind={streamKind}
                  />
                )}
            </li>
          );
        })}
      </ol>
      <div className="flow-add">
        <Button
          icon={<Plus size={13} />}
          onClick={() =>
            update((d) =>
              isSeq(d.getIn(path))
                ? d.addIn(
                    path,
                    d.createNode({ request: requests[0]?.path ?? "" }),
                  )
                : d.setIn(
                    path,
                    d.createNode([{ request: requests[0]?.path ?? "" }]),
                  ),
            )
          }
        >
          Request
        </Button>
        <Button
          icon={<Plus size={13} />}
          onClick={() =>
            update((d) =>
              d.addIn(
                path,
                d.createNode({ think: { min: "500ms", max: "1500ms" } }),
              ),
            )
          }
        >
          Think time
        </Button>
        <Button
          icon={<Plus size={13} />}
          onClick={() =>
            update((d) => {
              const step = d.createNode({
                script: `// Runs once per iteration, for every VU.\nconst res = pyro.request("${requests[0]?.path ?? "collection/request"}");\npyro.check(res, { "status is 2xx": (r) => r.status < 300 });\n`,
              });
              if (isSeq(d.getIn(path))) d.addIn(path, step);
              else d.setIn(path, d.createNode([step.toJSON()]));
            })
          }
        >
          Script
        </Button>
      </div>
    </div>
  );
}

function ThresholdsEditor({
  doc,
  update,
}: {
  doc: Document;
  update: (fn: (d: Document) => void) => void;
}) {
  const node = doc.get("thresholds");
  const rows: { key: string; i: number; cond: string; abort: boolean }[] = [];
  if (isMap(node)) {
    for (const pair of node.items) {
      const key = String((pair.key as { value?: unknown })?.value ?? pair.key);
      const val = pair.value;
      if (isSeq(val)) {
        val.items.forEach((c, i) => {
          if (isMap(c))
            rows.push({
              key,
              i,
              cond: String(c.get("threshold") ?? ""),
              abort: !!c.get("abortOnFail"),
            });
          else
            rows.push({
              key,
              i,
              cond: String((c as { value?: unknown })?.value ?? c),
              abort: false,
            });
        });
      }
    }
  }
  const setCond = (key: string, i: number, cond: string, abort: boolean) =>
    update((d) =>
      d.setIn(
        ["thresholds", key, i],
        abort ? d.createNode({ threshold: cond, abortOnFail: true }) : cond,
      ),
    );

  return (
    <section className="thresholds-ed">
      <h3 className="sub-head">
        Thresholds (the test fails if any is false at the end)
      </h3>
      <p className="lt-hint">
        Examples: <code>http_req_duration</code> · <code>p(95)&lt;500</code>,{" "}
        <code>http_req_failed</code> · <code>rate&lt;0.01</code>,{" "}
        <code>{"http_req_duration{request:shop/login}"}</code> ·{" "}
        <code>p(99)&lt;800</code>.
      </p>
      {rows.length > 0 && (
        <table className="lt-table">
          <thead>
            <tr>
              <th>Metric</th>
              <th>Condition</th>
              <th aria-label="Abort on fail" />
              <th className="lt-table-actions" aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={`${r.key}-${r.i}`}>
                <td className="mono lt-metric">{r.key}</td>
                <td>
                  <Input
                    className="mono"
                    value={r.cond}
                    onChange={(e) =>
                      setCond(r.key, r.i, e.target.value, r.abort)
                    }
                    aria-label={`Condition for ${r.key}`}
                  />
                </td>
                <td className="lt-nowrap">
                  <Checkbox
                    checked={r.abort}
                    onChange={(e) =>
                      setCond(r.key, r.i, r.cond, e.target.checked)
                    }
                  >
                    Abort on fail
                  </Checkbox>
                </td>
                <td className="lt-table-actions">
                  <Button
                    type="text"
                    icon={<X size={14} />}
                    aria-label="Remove threshold"
                    onClick={() =>
                      update((d) => {
                        d.deleteIn(["thresholds", r.key, r.i]);
                        const seq = d.getIn(["thresholds", r.key]);
                        if (isSeq(seq) && seq.items.length === 0)
                          d.deleteIn(["thresholds", r.key]);
                      })
                    }
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <NewThreshold
        onAdd={(key, cond) =>
          update((d) => {
            if (isSeq(d.getIn(["thresholds", key])))
              d.addIn(["thresholds", key], cond);
            else d.setIn(["thresholds", key], d.createNode([cond]));
          })
        }
      />
    </section>
  );
}

const METRIC_NAMES = [
  "http_req_duration",
  "http_req_failed",
  "http_reqs",
  "checks",
  "iteration_duration",
  "ws_connecting",
  "ws_errors",
  "sio_event_rtt",
  "dropped_iterations",
];

function NewThreshold({
  onAdd,
}: {
  onAdd: (key: string, cond: string) => void;
}) {
  const [key, setKey] = useState("http_req_duration");
  const [cond, setCond] = useState("p(95)<500");
  return (
    <div className="new-threshold">
      <AutoComplete
        className="nt-metric"
        value={key}
        onChange={(v) => setKey(v)}
        options={METRIC_NAMES.map((m) => ({ value: m }))}
        filterOption={(input, o) =>
          String(o?.value ?? "").includes(input.trim())
        }
      >
        <Input className="mono" aria-label="Metric" />
      </AutoComplete>
      <Input
        className="mono nt-cond"
        value={cond}
        onChange={(e) => setCond(e.target.value)}
        aria-label="Condition"
      />
      <Button
        icon={<Plus size={13} />}
        onClick={() =>
          key.trim() && cond.trim() && onAdd(key.trim(), cond.trim())
        }
      >
        Add threshold
      </Button>
    </div>
  );
}

// --- Runs list -------------------------------------------------------------

const STATUS_LABEL: Record<string, string> = {
  running: "Running",
  passed: "Passed",
  failed: "Failed",
  aborted: "Aborted",
  stopped: "Stopped",
};

type LoadRunRow = Awaited<ReturnType<typeof api.loadRuns>>[number];

export function RunsList({ test }: { test?: string }) {
  const select = useApp((s) => s.select);
  const q = useQuery({
    queryKey: ["loadruns", test ?? ""],
    queryFn: () => api.loadRuns(test),
    refetchInterval: 5000,
  });
  // Picked runs for comparison, oldest first once two are picked.
  const [picked, setPicked] = useState<string[]>([]);
  if (q.isPending) return <p className="muted">Loading…</p>;
  if (q.error) return <Alert type="error" showIcon title={q.error.message} />;
  if (q.data.length === 0)
    return (
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description="No runs yet. Press Start to run this test; results are kept here."
      />
    );
  const toggle = (id: string) =>
    setPicked((p) =>
      p.includes(id) ? p.filter((x) => x !== id) : [...p, id].slice(-2),
    );
  const compare = () => {
    const at = (id: string) => q.data.find((r) => r.id === id)?.startedAt ?? "";
    const [a, b] = [...picked].sort((x, y) => at(x).localeCompare(at(y)));
    select(`compare:${a}:${b}`);
  };
  const finished = q.data.filter((r) => r.status !== "running").length;
  const running = (r: LoadRunRow) => r.status === "running";
  const columns = [
    ...(finished >= 2
      ? [
          {
            key: "pick",
            title: <span className="sr-only">Compare</span>,
            className: "pick-col",
            width: 48,
            onCell: () => ({
              onClick: (e: React.MouseEvent) => e.stopPropagation(),
            }),
            render: (_: unknown, r: LoadRunRow) =>
              !running(r) && (
                <Checkbox
                  checked={picked.includes(r.id)}
                  onChange={() => toggle(r.id)}
                  aria-label={`Compare run from ${new Date(r.startedAt).toLocaleString()}`}
                />
              ),
          },
        ]
      : []),
    {
      key: "result",
      title: "Result",
      render: (_: unknown, r: LoadRunRow) => (
        <span className={`verdict v-${r.status}`}>
          {STATUS_LABEL[r.status] ?? r.status}
        </span>
      ),
    },
    {
      key: "started",
      title: "Started",
      render: (_: unknown, r: LoadRunRow) =>
        running(r) ? "now" : new Date(r.startedAt).toLocaleString(),
    },
    {
      key: "duration",
      title: "Duration",
      align: "right" as const,
      className: "mono",
      render: (_: unknown, r: LoadRunRow) =>
        running(r) ? "" : clock(r.duration),
    },
    {
      key: "requests",
      title: "Requests",
      align: "right" as const,
      render: (_: unknown, r: LoadRunRow) =>
        running(r) ? "" : r.requests.toLocaleString(),
    },
    {
      key: "p95",
      title: "p95",
      align: "right" as const,
      className: "mono",
      render: (_: unknown, r: LoadRunRow) =>
        !running(r) &&
        r.requests > 0 && (
          <>
            <span className={`swatch heat-${heatOf(r.p95)}`} aria-hidden />
            {formatMs(r.p95)}
          </>
        ),
    },
    {
      key: "failed",
      title: "Failed",
      align: "right" as const,
      render: (_: unknown, r: LoadRunRow) =>
        running(r) ? "" : `${(r.errorRate * 100).toFixed(2)}%`,
    },
    {
      key: "maxVUs",
      title: "Max VUs",
      align: "right" as const,
      render: (_: unknown, r: LoadRunRow) => (running(r) ? "" : r.maxVUs),
    },
  ];
  return (
    <div className="lt-runs">
      {finished >= 2 && (
        <div className="compare-bar">
          <span className="muted small">
            {picked.length === 2
              ? "Two runs picked."
              : "Pick two runs to compare them."}
          </span>
          <Button
            disabled={picked.length !== 2}
            onClick={compare}
            icon={<GitCompareArrows size={13} />}
          >
            Compare
          </Button>
        </div>
      )}
      <Table
        className="run-table"
        size="middle"
        rowKey="id"
        dataSource={q.data}
        columns={columns}
        pagination={false}
        scroll={{ x: "max-content" }}
        rowClassName={() => "run-row"}
        onRow={(r) => ({ onClick: () => select(`loadrun:${r.id}`) })}
      />
    </div>
  );
}

// --- New load test from a profile --------------------------------------------

/** Mini curves of VUs (or rate) over time for the profile cards. */
const PROFILE_CURVES: Record<ProfileId, string> = {
  smoke: "0,32 8,24 112,24 120,32",
  load: "0,32 30,8 96,8 120,32",
  stress: "0,32 18,22 38,22 52,13 72,13 86,4 106,4 120,32",
  spike: "0,28 46,28 52,4 62,4 68,28 120,28",
  soak: "0,32 10,14 110,14 120,32",
  breakpoint: "0,32 108,4 120,4",
};

export function NewLoadTest() {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const requests = useRequestPaths();
  const [title, setTitle] = useState("Checkout load");
  const [profile, setProfile] = useState<ProfileId>("load");
  const [target, setTarget] = useState(50);
  const [picked, setPicked] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const prof = PROFILES.find((p) => p.id === profile)!;
  const yamlPreview = generatePlan({
    title,
    requests: picked.length ? picked : ["<pick requests>"],
    profile,
    target,
  });

  const create = async () => {
    setError(null);
    const name = slugify(title);
    try {
      await api.saveLoadTest(
        name,
        generatePlan({ title, requests: picked, profile, target }),
      );
      await qc.invalidateQueries({ queryKey: ["loadtests"] });
      select(`load:${name}`);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <div className="page lt-page">
      <section className="card lt-header">
        <header className="lt-top">
          <div className="lt-title">
            <h1>New load test</h1>
            <p className="muted lt-note">
              Pick the requests each virtual user runs and a profile. You can
              change everything afterwards.
            </p>
          </div>
          <div className="lt-head-actions">
            <Button
              type="primary"
              className="lt-run"
              onClick={create}
              disabled={!picked.length || !title.trim()}
            >
              Create load test
            </Button>
          </div>
        </header>
        {error && (
          <Alert
            type="error"
            showIcon
            title="Couldn't create the load test"
            description={<pre className="mono pre-wrap lt-pre">{error}</pre>}
          />
        )}
      </section>

      <section className="card lt-section">
        <h2 className="lt-h2" id="lt-profile-label">
          Profile
        </h2>
        <Radio.Group
          className="profiles"
          value={profile}
          aria-labelledby="lt-profile-label"
          onChange={(e) => {
            const id = e.target.value as ProfileId;
            setProfile(id);
            setTarget(PROFILES.find((p) => p.id === id)!.defaultTarget);
          }}
        >
          {PROFILES.map((p) => (
            <Radio
              key={p.id}
              value={p.id}
              className={`profile-card ${profile === p.id ? "is-on" : ""}`}
            >
              <svg
                className="profile-curve"
                viewBox="0 0 120 36"
                preserveAspectRatio="none"
                aria-hidden
              >
                <polyline
                  points={PROFILE_CURVES[p.id]}
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinejoin="round"
                  vectorEffect="non-scaling-stroke"
                />
              </svg>
              <span className="profile-name">{p.name}</span>
              <span className="profile-desc">{p.summary}</span>
            </Radio>
          ))}
        </Radio.Group>
        <div className="lt-fields">
          <label className="lt-field span-2">
            <span className="lt-label">Name</span>
            <Input value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <label className="lt-field">
            <span className="lt-label">{prof.targetLabel}</span>
            <InputNumber
              min={1}
              className="lt-number"
              value={target}
              onChange={(n) => setTarget(Math.max(1, Number(n) || 1))}
            />
          </label>
        </div>
      </section>

      <div className="new-lt">
        <section className="card lt-section">
          <h2 className="lt-h2">Requests, in order</h2>
          {requests.length === 0 ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="No requests in the workspace yet."
            />
          ) : (
            <div className="pick-requests" role="group" aria-label="Requests, in order">
              {requests.map((r) => (
                <Checkbox
                  key={r.path}
                  checked={picked.includes(r.path)}
                  onChange={(e) =>
                    setPicked(
                      e.target.checked
                        ? [...picked, r.path]
                        : picked.filter((p) => p !== r.path),
                    )
                  }
                >
                  <span className="mono small">{r.path}</span>
                </Checkbox>
              ))}
            </div>
          )}
        </section>
        <section className="card lt-section new-lt-preview">
          <h2 className="lt-h2 mono">loadtests/{slugify(title)}.load.yaml</h2>
          <pre className="code-view mono">{yamlPreview}</pre>
        </section>
      </div>
    </div>
  );
}
