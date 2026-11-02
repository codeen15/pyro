import { Alert, Button, Dropdown, Input, InputNumber, Select, Switch, type MenuProps } from "antd";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "../icons";
import type { Assertion, Counter, Extractor, KeyValue, Processor, Timer } from "../api";
import { KVEditor } from "./KVEditor";
import { FieldsForm, usePluginTypes } from "./Plugins";

// JMeter-style pre/post processors and think time, for requests and for
// collection/folder settings. The engine runs, per level (collection →
// folders → request): legacy scripts.pre then `pre`; after the response,
// legacy scripts.post then `post`; finally the request's legacy assertions
// and extract. The helpers below fold the legacy fields into one list per
// phase in that same order, and write them back as lists on the first edit.

export type Phase = "pre" | "post";

// --- Assertion and extractor vocabularies ---------------------------------------

export const ASSERT_TYPES = [
  { value: "status", label: "Status code", needsPath: false, placeholder: "" },
  { value: "header", label: "Header", needsPath: true, placeholder: "Content-Type" },
  { value: "jsonpath", label: "JSON path", needsPath: true, placeholder: "$.data.id" },
  { value: "body", label: "Body text", needsPath: false, placeholder: "" },
  { value: "duration", label: "Response time (ms)", needsPath: false, placeholder: "" },
];

export const OPS = [
  { value: "eq", label: "equals" },
  { value: "neq", label: "not equals" },
  { value: "contains", label: "contains" },
  { value: "matches", label: "matches regex" },
  { value: "lt", label: "<" },
  { value: "lte", label: "≤" },
  { value: "gt", label: ">" },
  { value: "gte", label: "≥" },
  { value: "exists", label: "exists" },
  { value: "notexists", label: "doesn't exist" },
];

export const EXTRACT_FROM = [
  { value: "jsonpath", label: "JSON path", placeholder: "$.token" },
  { value: "header", label: "Header", placeholder: "X-Request-Id" },
  { value: "regex", label: "Regex (1st group)", placeholder: "id=(\\d+)" },
  { value: "boundary", label: "Between boundaries", placeholder: "" },
  { value: "xpath", label: "XPath", placeholder: "//order/id" },
  { value: "status", label: "Status code", placeholder: "" },
];

// --- Legacy fields ⇄ unified lists ----------------------------------------------

/** What requests and collection/folder settings have in common. */
export interface Processable {
  scripts?: { pre?: string; post?: string };
  pre?: Processor[];
  post?: Processor[];
  assertions?: Assertion[];
  extract?: Extractor[];
}

/** Legacy pre-request script (if any), then `pre`: the engine's order. */
export function preList(r: Processable): Processor[] {
  const s = r.scripts?.pre;
  return [...(s?.trim() ? [{ type: "script", script: s }] : []), ...(r.pre ?? [])];
}

/** Legacy post script, `post`, then legacy assertions and extractors: the engine's order. */
export function postList(r: Processable): Processor[] {
  const s = r.scripts?.post;
  return [
    ...(s?.trim() ? [{ type: "script", script: s }] : []),
    ...(r.post ?? []),
    ...(r.assertions ?? []).map((a): Processor => ({ type: "assert", assert: a })),
    ...(r.extract ?? []).map((e): Processor => ({ type: "extract", extract: e })),
  ];
}

/** Patch that stores the pre list, moving a legacy pre script into it. */
export function setPre<T extends Processable>(r: T, items: Processor[]): Partial<T> {
  return {
    pre: items.length ? items : undefined,
    scripts: r.scripts?.post ? { post: r.scripts.post } : undefined,
  } as Partial<T>;
}

/** Patch that stores the post list, moving legacy post script, assertions and extractors into it. */
export function setPost<T extends Processable>(r: T, items: Processor[]): Partial<T> {
  return {
    post: items.length ? items : undefined,
    assertions: undefined,
    extract: undefined,
    scripts: r.scripts?.pre ? { pre: r.scripts.pre } : undefined,
  } as Partial<T>;
}

// --- Processor list ---------------------------------------------------------------

type PluginProc = ReturnType<typeof usePluginTypes>[number];

const BUILTIN: { type: string; label: string; hint?: string; phases: Phase[]; make: () => Processor }[] = [
  { type: "script", label: "Script", hint: "JavaScript with pm.*", phases: ["pre", "post"], make: () => ({ type: "script", script: "" }) },
  { type: "vars", label: "Set variables", hint: "like JMeter User Parameters", phases: ["pre", "post"], make: () => ({ type: "vars", vars: [] }) },
  { type: "counter", label: "Counter", hint: "a number per virtual user", phases: ["pre"], make: () => ({ type: "counter", counter: { var: "", start: 1, step: 1 } }) },
  {
    type: "extract",
    label: "Extract a value",
    hint: "JSON path, regex, header, boundary, XPath",
    phases: ["post"],
    make: () => ({ type: "extract", extract: { var: "", from: "jsonpath", path: "" } }),
  },
  { type: "assert", label: "Assertion", hint: "check status, a header or the body", phases: ["post"], make: () => ({ type: "assert", assert: { type: "status", op: "eq", value: "200" } }) },
];

function typeLabel(type: string, plugins: PluginProc[]): string {
  const b = BUILTIN.find((x) => x.type === type);
  if (b) return b.label;
  if (type.startsWith("plugin:")) {
    const id = type.slice(7);
    return plugins.find((p) => p.id === id)?.title || id;
  }
  return type;
}

function typeTag(type: string): string {
  return type.startsWith("plugin:") ? type.slice(7) : type;
}

const allowsPhase = (p: PluginProc, phase: Phase) => !p.phases?.length || p.phases.includes(phase);

export function ProcessorList({ phase, items, onChange }: { phase: Phase; items: Processor[]; onChange: (items: Processor[]) => void }) {
  const plugins = usePluginTypes("processor");
  const set = (i: number, p: Processor) => onChange(items.map((x, j) => (j === i ? p : x)));
  const move = (i: number, d: number) => {
    const next = items.slice();
    [next[i], next[i + d]] = [next[i + d], next[i]];
    onChange(next);
  };
  const remove = (i: number) => onChange(items.filter((_, j) => j !== i));

  const builtins = BUILTIN.filter((b) => b.phases.includes(phase));
  const pluginOpts = plugins.filter((p) => allowsPhase(p, phase));
  const addItem = (label: string, hint?: string) => (
    <span className="proc-add-item">
      <span>{label}</span>
      {hint && <span className="proc-add-hint">{hint}</span>}
    </span>
  );
  const menu: MenuProps["items"] = [
    ...builtins.map((b) => ({ key: b.type, label: addItem(b.label, b.hint) })),
    ...(pluginOpts.length
      ? [
          {
            type: "group" as const,
            label: "Plugins",
            children: pluginOpts.map((p) => ({
              key: `plugin:${p.id}`,
              label: addItem(p.title || p.id, p.plugin.ready ? p.plugin.title || p.plugin.name : "not ready"),
            })),
          },
        ]
      : []),
  ];
  const add = (key: string) => {
    const b = BUILTIN.find((x) => x.type === key);
    onChange([...items, b ? b.make() : { type: key }]);
  };
  const noun = phase === "pre" ? "pre-processor" : "post-processor";

  return (
    <div className="proc-list">
      {items.length === 0 ? (
        <p className="muted small proc-empty">
          {phase === "pre"
            ? "Pre-processors prepare the request before it's sent: set variables, count, or run a script."
            : "Post-processors read the response: extract values into variables, assert, or run a script."}{" "}
          They run collection first, then each folder, then the request, each list top to bottom.
        </p>
      ) : (
        <p className="muted small proc-order">Runs after the collection's and folders' {noun}s, top to bottom.</p>
      )}
      {items.map((p, i) => {
        const def = typeLabel(p.type, plugins);
        const label = p.name?.trim() || def;
        return (
          <section key={i} className={p.disabled ? "proc-card is-off" : "proc-card"} aria-label={`${noun} ${i + 1}: ${label}`}>
            <header className="proc-head">
              <span className="proc-num mono">{i + 1}</span>
              <span className="proc-type mono">{typeTag(p.type)}</span>
              <Input
                variant="borderless"
                className="proc-name"
                value={p.name ?? ""}
                placeholder={def}
                onChange={(e) => set(i, { ...p, name: e.target.value || undefined })}
                aria-label={`Name of ${noun} ${i + 1}`}
                spellCheck={false}
              />
              <span className="proc-tools">
                <Switch size="small" checked={!p.disabled} onChange={(on) => set(i, { ...p, disabled: on ? undefined : true })} aria-label={`Enable ${label}`} />
                <Button type="text" size="small" icon={<ArrowUp size={13} />} disabled={i === 0} onClick={() => move(i, -1)} aria-label={`Move ${label} up`} title="Move up" />
                <Button
                  type="text"
                  size="small"
                  icon={<ArrowDown size={13} />}
                  disabled={i === items.length - 1}
                  onClick={() => move(i, 1)}
                  aria-label={`Move ${label} down`}
                  title="Move down"
                />
                <Button type="text" size="small" icon={<Trash2 size={13} />} onClick={() => remove(i)} aria-label={`Remove ${label}`} title="Remove" />
              </span>
            </header>
            <div className="proc-body">
              <ProcessorBody phase={phase} p={p} onChange={(np) => set(i, np)} plugins={plugins} />
            </div>
          </section>
        );
      })}
      <Dropdown trigger={["click"]} menu={{ items: menu, onClick: ({ key }) => add(key) }}>
        <Button type="dashed" icon={<Plus size={13} />} className="proc-add">
          Add {noun}
        </Button>
      </Dropdown>
    </div>
  );
}

function ProcessorBody({ phase, p, onChange, plugins }: { phase: Phase; p: Processor; onChange: (p: Processor) => void; plugins: PluginProc[] }) {
  if (p.type === "script")
    return (
      <>
        <Input.TextArea
          className="code-input mono"
          autoSize={{ minRows: 4 }}
          value={p.script ?? ""}
          onChange={(e) => onChange({ ...p, script: e.target.value })}
          spellCheck={false}
          aria-label="Script"
          placeholder={phase === "pre" ? 'pm.variables.set("ts", Date.now());' : 'pm.test("status is 200", () => {\n  pm.response.to.have.status(200);\n});'}
        />
        <p className="muted small proc-hint">
          JavaScript with Postman's <code>pm.*</code> API ({phase === "pre" ? <code>pm.variables</code> : <code>pm.response</code>}, <code>pm.environment</code>,{" "}
          <code>pm.test</code>, <code>pm.sendRequest</code>…).
        </p>
      </>
    );
  if (p.type === "vars")
    return (
      <>
        <KVEditor label="Variables" keyPlaceholder="Variable" rows={p.vars ?? []} onChange={(vars: KeyValue[]) => onChange({ ...p, vars })} />
        <p className="muted small proc-hint">
          Values can use variables and functions like <code>{"{{$randomInt}}"}</code>.
        </p>
      </>
    );
  if (p.type === "counter") return <CounterFields c={p.counter ?? { var: "" }} onChange={(counter) => onChange({ ...p, counter })} />;
  if (p.type === "extract") return <ExtractFields x={p.extract ?? { var: "", from: "jsonpath" }} onChange={(extract) => onChange({ ...p, extract })} />;
  if (p.type === "assert") return <AssertFields a={p.assert ?? { type: "status" }} onChange={(assert) => onChange({ ...p, assert })} />;
  if (p.type.startsWith("plugin:")) {
    const id = p.type.slice(7);
    const prov = plugins.find((x) => x.id === id);
    return (
      <>
        {!prov && (
          <Alert
            type="warning"
            showIcon
            title={
              <>
                No installed plugin provides <code>{p.type}</code>. Install it from the Plugins tab; the processor is kept as written.
              </>
            }
          />
        )}
        {prov && !prov.plugin.ready && (
          <Alert type="warning" showIcon title={`The ${prov.plugin.title || prov.plugin.name} plugin is ${prov.plugin.approved ? "disabled" : "waiting for approval"}. Open it in the Plugins tab.`} />
        )}
        {prov && !allowsPhase(prov, phase) && <Alert type="warning" showIcon title={`This plugin doesn't run as a ${phase}-processor.`} />}
        {prov && (
          <FieldsForm fields={prov.fields ?? []} values={p.options} onChange={(options) => onChange({ ...p, options: Object.keys(options).length ? options : undefined })} label="processor" />
        )}
      </>
    );
  }
  return <Alert type="warning" showIcon title={`Unknown processor type “${p.type}”; it is kept as written.`} />;
}

const num = (v: number | string | null) => (v === null || v === "" ? undefined : Number(v));

function CounterFields({ c, onChange }: { c: Counter; onChange: (c: Counter) => void }) {
  const set = (patch: Partial<Counter>) => onChange({ ...c, ...patch });
  return (
    <>
      <div className="proc-grid">
        <label>
          <span className="field-label">Variable</span>
          <Input className="mono" value={c.var} onChange={(e) => set({ var: e.target.value })} placeholder="i" aria-label="Counter variable" spellCheck={false} />
        </label>
        <label>
          <span className="field-label">Start</span>
          <InputNumber className="mono" value={c.start} onChange={(v) => set({ start: num(v) })} placeholder="0" aria-label="Start" />
        </label>
        <label>
          <span className="field-label">Step</span>
          <InputNumber className="mono" value={c.step} onChange={(v) => set({ step: num(v) })} placeholder="1" aria-label="Step" />
        </label>
        <label>
          <span className="field-label">End</span>
          <InputNumber className="mono" value={c.end} onChange={(v) => set({ end: num(v) })} placeholder="none" aria-label="End" />
        </label>
        <label>
          <span className="field-label">Format</span>
          <Input className="mono" value={c.format ?? ""} onChange={(e) => set({ format: e.target.value || undefined })} placeholder="%d" aria-label="Format" spellCheck={false} />
        </label>
      </div>
      <p className="muted small proc-hint">
        Counts per virtual user and wraps back to Start after passing End (0 or empty: never). Format is printf-style, e.g. <code>%05d</code>.
      </p>
    </>
  );
}

function ExtractFields({ x, onChange }: { x: Extractor; onChange: (x: Extractor) => void }) {
  const set = (patch: Partial<Extractor>) => onChange({ ...x, ...patch });
  const src = EXTRACT_FROM.find((f) => f.value === x.from) ?? EXTRACT_FROM[0];
  return (
    <div className="proc-grid">
      <label>
        <span className="field-label">From</span>
        <Select
          value={x.from}
          onChange={(from) => set({ from, path: from === "boundary" || from === "status" ? undefined : x.path, left: undefined, right: undefined })}
          aria-label="Extract from"
          popupMatchSelectWidth={false}
          options={EXTRACT_FROM.map((f) => ({ value: f.value, label: f.label }))}
        />
      </label>
      {x.from === "boundary" ? (
        <>
          <label>
            <span className="field-label">Left</span>
            <Input className="mono" value={x.left ?? ""} onChange={(e) => set({ left: e.target.value })} placeholder={'id="'} aria-label="Left boundary" spellCheck={false} />
          </label>
          <label>
            <span className="field-label">Right</span>
            <Input className="mono" value={x.right ?? ""} onChange={(e) => set({ right: e.target.value })} placeholder={'"'} aria-label="Right boundary" spellCheck={false} />
          </label>
        </>
      ) : (
        x.from !== "status" && (
          <label className="proc-wide">
            <span className="field-label">{src.value === "xpath" ? "XPath" : src.value === "header" ? "Header" : src.value === "regex" ? "Regex" : "Path"}</span>
            <Input className="mono" value={x.path ?? ""} onChange={(e) => set({ path: e.target.value })} placeholder={src.placeholder} aria-label="Extract path" spellCheck={false} />
          </label>
        )
      )}
      <label>
        <span className="field-label">Into variable</span>
        <Input className="mono" value={x.var} onChange={(e) => set({ var: e.target.value })} placeholder="name" aria-label="Into variable" spellCheck={false} />
      </label>
    </div>
  );
}

function AssertFields({ a, onChange }: { a: Assertion; onChange: (a: Assertion) => void }) {
  const set = (patch: Partial<Assertion>) => onChange({ ...a, ...patch });
  const t = ASSERT_TYPES.find((x) => x.value === a.type) ?? ASSERT_TYPES[0];
  const op = a.op || (a.value ? "eq" : t.needsPath ? "exists" : "eq");
  const noValue = op === "exists" || op === "notexists";
  return (
    <div className="proc-grid">
      <label>
        <span className="field-label">Check</span>
        <Select
          value={a.type}
          onChange={(type) => set({ type, path: undefined })}
          aria-label="Check"
          popupMatchSelectWidth={false}
          options={ASSERT_TYPES.map((x) => ({ value: x.value, label: x.label }))}
        />
      </label>
      {t.needsPath && (
        <label className="proc-wide">
          <span className="field-label">{t.value === "header" ? "Header" : "Path"}</span>
          <Input className="mono" value={a.path ?? ""} placeholder={t.placeholder} onChange={(e) => set({ path: e.target.value })} aria-label="Assertion path" spellCheck={false} />
        </label>
      )}
      <label>
        <span className="field-label">Operator</span>
        <Select value={op} onChange={(v) => set({ op: v })} aria-label="Operator" popupMatchSelectWidth={false} options={OPS} />
      </label>
      {!noValue && (
        <label>
          <span className="field-label">Expected</span>
          <Input className="mono" value={a.value ?? ""} onChange={(e) => set({ value: e.target.value })} aria-label="Expected value" spellCheck={false} />
        </label>
      )}
    </div>
  );
}

// --- Think time ---------------------------------------------------------------------

const DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;
const UNIT_MS: Record<string, number> = { ns: 1e-6, us: 1e-3, µs: 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 };

function durationMs(s: string): number | null {
  if (/^\d+(\.\d+)?$/.test(s)) return Number(s); // a bare number is milliseconds, as in JMeter
  if (!DURATION.test(s)) return null;
  let ms = 0;
  for (const m of s.matchAll(/(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g)) ms += Number(m[1]) * UNIT_MS[m[2]];
  return ms;
}

const trim = (n: number) => String(Number(n.toFixed(2)));

/** "500ms" → "500 ms", "1.5s" → "1.5 s"; unparsable values come back as written. */
function humanDuration(s: string): string {
  const ms = durationMs(s.trim());
  if (ms === null) return s;
  if (ms < 1000) return `${trim(ms)} ms`;
  if (ms < 60_000) return `${trim(ms / 1000)} s`;
  return `${trim(ms / 60_000)} min`;
}

const TIMER_TYPES: { value: Timer["type"]; label: string }[] = [
  { value: "none", label: "No think time" },
  { value: "constant", label: "Constant" },
  { value: "uniform", label: "Uniform random" },
  { value: "gaussian", label: "Gaussian random" },
  { value: "poisson", label: "Poisson random" },
];

const RANGE_LABEL: Partial<Record<Timer["type"], string>> = { uniform: "Random up to", gaussian: "Deviation", poisson: "Mean (λ)" };

function timerSummary(t: Timer | undefined, target: string): string {
  if (!t) return "";
  if (t.type === "none") return `No think time, even if a folder or the collection sets one.`;
  const d = t.delay?.trim() ? humanDuration(t.delay.trim()) : "";
  const r = t.range?.trim() ? humanDuration(t.range.trim()) : "";
  let wait: string;
  switch (t.type) {
    case "constant":
      if (!d) return "Set a delay, e.g. 500ms.";
      wait = d;
      break;
    case "uniform":
      if (!r) wait = d || "0 ms";
      else wait = d ? `${d} + 0–${r}` : `0–${r}`;
      break;
    case "gaussian":
      wait = r ? `${d || "0 ms"} ± ${r}` : d || "0 ms";
      break;
    case "poisson":
      wait = r ? `${d ? `${d} + ` : ""}a random delay averaging ${r}` : d || "0 ms";
      break;
    default:
      return "";
  }
  return `Waits ${wait} before ${target} in load tests`;
}

export function TimerEditor({
  value,
  onChange,
  inheritLabel,
  target = "the request",
}: {
  value?: Timer;
  onChange: (t?: Timer) => void;
  inheritLabel: string;
  /** Who waits, for the summary: "the request" or "each request". */
  target?: string;
}) {
  const type = value?.type ?? "inherit";
  const showRange = !!value && value.type !== "constant" && value.type !== "none";
  const set = (patch: Partial<Timer>) => onChange({ ...value!, ...patch });
  const pick = (v: string) => {
    if (v === "inherit") return onChange(undefined);
    const t = v as Timer["type"];
    if (t === "none") return onChange({ type: "none" });
    onChange({ type: t, delay: value?.delay, range: t === "constant" ? undefined : value?.range });
  };
  const bad = [value?.delay, showRange ? value?.range : undefined].filter((s): s is string => !!s?.trim() && durationMs(s.trim()) === null);
  const summary = timerSummary(value, target);

  return (
    <div className="timer-editor">
      <div className="timer-row">
        <Select
          className="timer-type"
          value={type}
          onChange={pick}
          aria-label="Think time"
          popupMatchSelectWidth={false}
          options={[{ value: "inherit", label: inheritLabel }, ...TIMER_TYPES]}
        />
        {value && value.type !== "none" && (
          <label className="timer-field">
            <span className="field-label">Delay</span>
            <Input
              className="mono"
              value={value.delay ?? ""}
              onChange={(e) => set({ delay: e.target.value || undefined })}
              placeholder="500ms"
              aria-label="Delay"
              spellCheck={false}
              autoComplete="off"
            />
          </label>
        )}
        {showRange && (
          <label className="timer-field">
            <span className="field-label">{RANGE_LABEL[value.type]}</span>
            <Input
              className="mono"
              value={value.range ?? ""}
              onChange={(e) => set({ range: e.target.value || undefined })}
              placeholder="500ms"
              aria-label={RANGE_LABEL[value.type]}
              spellCheck={false}
              autoComplete="off"
            />
          </label>
        )}
      </div>
      {summary && <p className="muted small timer-summary">{summary}</p>}
      {bad.map((s) => (
        <p key={s} className="inline-warn small">
          “{s}” doesn't look like a duration; use e.g. 500ms, 1.5s or 2m.
        </p>
      ))}
    </div>
  );
}
