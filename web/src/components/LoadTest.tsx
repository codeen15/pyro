import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDown,
  ArrowUp,
  GitCompareArrows,
  Play,
  Plus,
  Save,
  ShieldAlert,
  Trash2,
  X,
} from "lucide-react";
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
    const out: { path: string; name: string; kind?: string }[] = [];
    const walk = (n: TreeNode) =>
      n.type === "request"
        ? out.push({ path: n.path, name: n.name, kind: n.kind })
        : n.children?.forEach(walk);
    ws.data?.collections.forEach(walk);
    return out;
  }, [ws.data]);
}

type Tab = "designer" | "yaml" | "runs";

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
  if (q.error) return <div className="pad inline-error">{q.error.message}</div>;
  const v = q.data;

  return (
    <section className="loadtest">
      <header className="lt-head">
        <div>
          <h1>{v.title || name}</h1>
          <p className="muted small mono">
            loadtests/{name}.load.yaml
            {dirty && <span className="dirty"> · unsaved</span>}
            {v.peakVUs ? <span> · up to {v.peakVUs} VUs</span> : null}
          </p>
        </div>
        <div className="lt-actions">
          <button
            className="btn ghost"
            onClick={() => save.mutate()}
            disabled={!dirty || save.isPending}
            title="Save (Ctrl+S)"
          >
            <Save size={14} />{" "}
            {save.isPending ? "Saving…" : dirty ? "Save" : "Saved"}
          </button>
          {agentCount > 0 && (
            <select
              className="run-on"
              value={where}
              onChange={(e) => {
                setWhere(e.target.value as "local" | "agents");
                agents.refetch();
              }}
              aria-label="Run on"
            >
              <option value="local">This machine</option>
              <option value="agents">
                {agentCount} agent{agentCount === 1 ? "" : "s"}
              </option>
            </select>
          )}
          <button
            className="btn primary"
            onClick={() => start(false)}
            disabled={!!v.error && !dirty}
          >
            <Play size={14} /> Start
          </button>
        </div>
      </header>
      <p className="muted small">
        Environment:{" "}
        {environment ? <code>{environment}</code> : "none selected"} (from the
        top bar). Only load test systems you own or are allowed to test.
      </p>
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
      {save.error && <p className="inline-error">{save.error.message}</p>}
      {!dirty && v.error && (
        <pre className="inline-error mono pre-wrap">{v.error}</pre>
      )}
      {startError && <p className="inline-error">{startError}</p>}
      {confirm && (
        <div className="confirm-hosts" role="alert">
          <ShieldAlert size={18} />
          <div>
            <p>
              <strong>This test will send traffic to:</strong>{" "}
              {confirm.join(", ")}
            </p>
            <p className="muted small">
              These hosts aren't on this machine or in{" "}
              <code>loadAllowHosts</code> in pyro.yaml. Make sure you're allowed
              to load test them.
            </p>
            <div className="confirm-actions">
              <button className="btn primary" onClick={() => start(true)}>
                Send traffic to{" "}
                {confirm.length === 1 ? "this host" : "these hosts"}
              </button>
              <button className="btn ghost" onClick={() => setConfirm(null)}>
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="tabs" role="tablist">
        {(["designer", "yaml", "runs"] as Tab[]).map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            className="tab"
            onClick={() => setTab(t)}
          >
            {t === "yaml" ? "YAML" : t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>
      <div className="tab-panel">
        {tab === "designer" && <Designer text={yamlText} onChange={setText} />}
        {tab === "yaml" && (
          <textarea
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
      <p className="inline-error">
        The YAML doesn't parse ({doc.errors[0].message}). Fix it in the YAML
        tab.
      </p>
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
      <label className="field-row">
        <span className="field-label">Name</span>
        <input
          className="cell wide"
          value={String(doc.get("name") ?? "")}
          onChange={(e) => update((d) => d.set("name", e.target.value))}
        />
      </label>
      <label className="field-row">
        <span className="field-label">Max requests/s</span>
        <input
          className="cell mono"
          inputMode="decimal"
          value={doc.get("maxRPS") === undefined ? "" : String(doc.get("maxRPS"))}
          placeholder="no cap"
          aria-label="Max requests per second"
          onChange={(e) =>
            update((d) => {
              const n = Number(e.target.value);
              if (e.target.value.trim() === "" || !Number.isFinite(n) || n <= 0) d.delete("maxRPS");
              else d.set("maxRPS", n);
            })
          }
        />
        <span className="muted small">Spaces request starts across all VUs; waiting isn't counted as response time.</span>
      </label>

      {names.map((sn) => (
        <ScenarioEditor
          key={sn}
          name={sn}
          doc={doc}
          update={update}
          requests={requests}
        />
      ))}
      <button
        className="btn ghost small"
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
        <Plus size={13} /> Add scenario
      </button>

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
  requests: { path: string; name: string; kind?: string }[];
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
    <label className="sc-field">
      <span className="field-label">{label}</span>
      <input
        className="cell mono"
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
        <input
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
        <select
          value={executor}
          onChange={(e) => set("executor", e.target.value)}
          aria-label="Executor"
        >
          {EXECUTORS.map((x) => (
            <option key={x.id} value={x.id}>
              {x.label}
            </option>
          ))}
        </select>
        <button
          className="icon-btn"
          onClick={() => update((d) => d.deleteIn(["scenarios", name]))}
          aria-label={`Remove scenario ${name}`}
          title="Remove scenario"
        >
          <Trash2 size={14} />
        </button>
      </header>

      <div className="sc-fields">
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
          <table className="rule-table stage-table">
            <thead>
              <tr>
                <th>Duration</th>
                <th>
                  {executor === "ramping-vus" ? "Target VUs" : "Target rate"}
                </th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {stages.map((s, i) => (
                <tr key={i}>
                  <td>
                    <input
                      className="cell mono"
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
                    <input
                      className="cell mono"
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
                  <td className="kv-actions">
                    <button
                      className="icon-btn"
                      onClick={() =>
                        update((d) => d.deleteIn([...base, "stages", i]))
                      }
                      aria-label={`Remove stage ${i + 1}`}
                    >
                      <X size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <button
            className="btn ghost small"
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
            <Plus size={13} /> Add stage
          </button>
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
  requests: { path: string; name: string; kind?: string }[];
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
          let body: React.ReactNode;
          if (request !== undefined) {
            const known = requests.some((r) => r.path === request);
            body = (
              <>
                <span className="step-kind">Request</span>
                <select
                  value={request}
                  onChange={(e) =>
                    update((d) =>
                      d.setIn([...path, i, "request"], e.target.value),
                    )
                  }
                  aria-label="Request"
                >
                  {!known && (
                    <option value={request}>
                      {request || "Choose a request"} (missing)
                    </option>
                  )}
                  {requests.map((r) => (
                    <option key={r.path} value={r.path}>
                      {r.path}
                      {r.kind && r.kind !== "http" ? ` (${r.kind})` : ""}
                    </option>
                  ))}
                </select>
                <label className="check">
                  <input
                    type="checkbox"
                    checked={!!m.get("once")}
                    onChange={(e) =>
                      update((d) =>
                        e.target.checked
                          ? d.setIn([...path, i, "once"], true)
                          : d.deleteIn([...path, i, "once"]),
                      )
                    }
                  />
                  Once per VU
                </label>
                {m.get("hold") !== undefined && (
                  <span className="muted small">
                    holds {String(m.get("hold"))}
                  </span>
                )}
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
                <input
                  className="cell mono dur"
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
                <input
                  className="cell mono dur"
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
            <li key={i} className="flow-step">
              <span className="step-no">{i + 1}</span>
              {body}
              <span className="step-actions">
                <button
                  className="icon-btn"
                  onClick={() => move(i, -1)}
                  aria-label="Move up"
                  disabled={i === 0}
                >
                  <ArrowUp size={13} />
                </button>
                <button
                  className="icon-btn"
                  onClick={() => move(i, 1)}
                  aria-label="Move down"
                  disabled={i === items.length - 1}
                >
                  <ArrowDown size={13} />
                </button>
                <button
                  className="icon-btn"
                  onClick={() => update((d) => d.deleteIn([...path, i]))}
                  aria-label="Remove step"
                >
                  <X size={13} />
                </button>
              </span>
            </li>
          );
        })}
      </ol>
      <div className="flow-add">
        <button
          className="btn ghost small"
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
          <Plus size={13} /> Request
        </button>
        <button
          className="btn ghost small"
          onClick={() =>
            update((d) =>
              d.addIn(
                path,
                d.createNode({ think: { min: "500ms", max: "1500ms" } }),
              ),
            )
          }
        >
          <Plus size={13} /> Think time
        </button>
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
      <p className="muted small">
        Examples: <code>http_req_duration</code> · <code>p(95)&lt;500</code>,{" "}
        <code>http_req_failed</code> · <code>rate&lt;0.01</code>,{" "}
        <code>{"http_req_duration{request:shop/login}"}</code> ·{" "}
        <code>p(99)&lt;800</code>.
      </p>
      {rows.length > 0 && (
        <table className="rule-table">
          <tbody>
            {rows.map((r) => (
              <tr key={`${r.key}-${r.i}`}>
                <td className="mono">{r.key}</td>
                <td>
                  <input
                    className="cell mono"
                    value={r.cond}
                    onChange={(e) =>
                      setCond(r.key, r.i, e.target.value, r.abort)
                    }
                    aria-label={`Condition for ${r.key}`}
                  />
                </td>
                <td>
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={r.abort}
                      onChange={(e) =>
                        setCond(r.key, r.i, r.cond, e.target.checked)
                      }
                    />
                    Abort on fail
                  </label>
                </td>
                <td className="kv-actions">
                  <button
                    className="icon-btn"
                    aria-label="Remove threshold"
                    onClick={() =>
                      update((d) => {
                        d.deleteIn(["thresholds", r.key, r.i]);
                        const seq = d.getIn(["thresholds", r.key]);
                        if (isSeq(seq) && seq.items.length === 0)
                          d.deleteIn(["thresholds", r.key]);
                      })
                    }
                  >
                    <X size={14} />
                  </button>
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

function NewThreshold({
  onAdd,
}: {
  onAdd: (key: string, cond: string) => void;
}) {
  const [key, setKey] = useState("http_req_duration");
  const [cond, setCond] = useState("p(95)<500");
  return (
    <div className="new-threshold">
      <input
        className="cell mono"
        value={key}
        onChange={(e) => setKey(e.target.value)}
        aria-label="Metric"
        list="metric-names"
      />
      <datalist id="metric-names">
        {[
          "http_req_duration",
          "http_req_failed",
          "http_reqs",
          "checks",
          "iteration_duration",
          "ws_connecting",
          "ws_errors",
          "sio_event_rtt",
          "dropped_iterations",
        ].map((m) => (
          <option key={m} value={m} />
        ))}
      </datalist>
      <input
        className="cell mono"
        value={cond}
        onChange={(e) => setCond(e.target.value)}
        aria-label="Condition"
      />
      <button
        className="btn ghost small"
        onClick={() =>
          key.trim() && cond.trim() && onAdd(key.trim(), cond.trim())
        }
      >
        <Plus size={13} /> Add threshold
      </button>
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
  if (q.error) return <p className="inline-error">{q.error.message}</p>;
  if (q.data.length === 0)
    return (
      <p className="muted">
        No runs yet. Press Start to run this test; results are kept here.
      </p>
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
  return (
    <>
      {finished >= 2 && (
        <div className="compare-bar">
          <span className="muted small">
            {picked.length === 2
              ? "Two runs picked."
              : "Pick two runs to compare them."}
          </span>
          <button
            className="btn ghost small"
            disabled={picked.length !== 2}
            onClick={compare}
          >
            <GitCompareArrows size={13} /> Compare
          </button>
        </div>
      )}
      <table className="data-table run-table">
        <thead>
          <tr>
            {finished >= 2 && (
              <th className="pick-col">
                <span className="sr-only">Compare</span>
              </th>
            )}
            <th>Result</th>
            <th>Started</th>
            <th className="num">Duration</th>
            <th className="num">Requests</th>
            <th className="num">p95</th>
            <th className="num">Failed</th>
            <th className="num">Max VUs</th>
          </tr>
        </thead>
        <tbody>
          {q.data.map((r) => (
            <tr
              key={r.id}
              className="run-row"
              onClick={() => select(`loadrun:${r.id}`)}
            >
              {finished >= 2 && (
                <td className="pick-col" onClick={(e) => e.stopPropagation()}>
                  {r.status !== "running" && (
                    <input
                      type="checkbox"
                      checked={picked.includes(r.id)}
                      onChange={() => toggle(r.id)}
                      aria-label={`Compare run from ${new Date(r.startedAt).toLocaleString()}`}
                    />
                  )}
                </td>
              )}
              <td>
                <span className={`verdict v-${r.status}`}>
                  {STATUS_LABEL[r.status] ?? r.status}
                </span>
              </td>
              <td>
                {r.status === "running"
                  ? "now"
                  : new Date(r.startedAt).toLocaleString()}
              </td>
              <td className="num mono">
                {r.status === "running" ? "" : clock(r.duration)}
              </td>
              <td className="num">
                {r.status === "running" ? "" : r.requests.toLocaleString()}
              </td>
              <td className="num mono">
                {r.status !== "running" && r.requests > 0 && (
                  <>
                    <span
                      className={`swatch heat-${heatOf(r.p95)}`}
                      aria-hidden
                    />
                    {formatMs(r.p95)}
                  </>
                )}
              </td>
              <td className="num">
                {r.status === "running"
                  ? ""
                  : `${(r.errorRate * 100).toFixed(2)}%`}
              </td>
              <td className="num">{r.status === "running" ? "" : r.maxVUs}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

// --- New load test from a profile --------------------------------------------

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
    <section className="loadtest">
      <header className="lt-head">
        <div>
          <h1>New load test</h1>
          <p className="muted small">
            Pick the requests each virtual user runs and a profile. You can
            change everything afterwards.
          </p>
        </div>
      </header>
      <div className="new-lt">
        <div className="new-lt-form">
          <label className="field-row">
            <span className="field-label">Name</span>
            <input
              className="cell wide"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </label>
          <fieldset className="profiles">
            <legend className="field-label">Profile</legend>
            {PROFILES.map((p) => (
              <label
                key={p.id}
                className={`profile ${profile === p.id ? "is-on" : ""}`}
              >
                <input
                  type="radio"
                  name="profile"
                  checked={profile === p.id}
                  onChange={() => {
                    setProfile(p.id);
                    setTarget(p.defaultTarget);
                  }}
                />
                <span>
                  <strong>{p.name}</strong>
                  <span className="muted small">{p.summary}</span>
                </span>
              </label>
            ))}
          </fieldset>
          <label className="field-row">
            <span className="field-label">{prof.targetLabel}</span>
            <input
              type="number"
              min={1}
              className="cell num-input"
              value={target}
              onChange={(e) =>
                setTarget(Math.max(1, Number(e.target.value) || 1))
              }
            />
          </label>
          <fieldset className="pick-requests">
            <legend className="field-label">Requests, in order</legend>
            {requests.length === 0 && (
              <p className="muted small">No requests in the workspace yet.</p>
            )}
            {requests.map((r) => (
              <label key={r.path} className="check">
                <input
                  type="checkbox"
                  checked={picked.includes(r.path)}
                  onChange={(e) =>
                    setPicked(
                      e.target.checked
                        ? [...picked, r.path]
                        : picked.filter((p) => p !== r.path),
                    )
                  }
                />
                <span className="mono small">{r.path}</span>
              </label>
            ))}
          </fieldset>
          {error && <pre className="inline-error mono pre-wrap">{error}</pre>}
          <button
            className="btn primary"
            onClick={create}
            disabled={!picked.length || !title.trim()}
          >
            Create load test
          </button>
        </div>
        <div className="new-lt-preview">
          <span className="field-label">
            loadtests/{slugify(title)}.load.yaml
          </span>
          <pre className="code-view mono">{yamlPreview}</pre>
        </div>
      </div>
    </section>
  );
}
