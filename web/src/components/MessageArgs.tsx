// Postman-style message composers. Socket.IO messages are an event plus a
// list of typed arguments; they're stored as the JSON array the engine
// sends, so files stay compatible. {{variables}} may stand in for a whole
// number, boolean or JSON argument: they're resolved before the array is
// parsed, at send time.
import { useState } from "react";
import { Alert, Button, Segmented, Select, Switch } from "antd";
import { VarInput, VarTextArea } from "../vars";
import { ArrowDown, ArrowUp, Plus, X } from "../icons";

export type ArgType = "text" | "json" | "number" | "boolean" | "binary";
export interface Arg {
  type: ArgType;
  value: string;
}

const TYPES: { value: ArgType; label: string }[] = [
  { value: "text", label: "Text" },
  { value: "json", label: "JSON" },
  { value: "number", label: "Number" },
  { value: "boolean", label: "Boolean" },
  { value: "binary", label: "Binary" },
];

/** Splits a JSON array's text into its top-level elements, tolerating {{vars}} in place of values. */
function splitTopLevel(src: string): string[] | null {
  const s = src.trim();
  if (!s.startsWith("[") || !s.endsWith("]")) return null;
  const body = s.slice(1, -1);
  const out: string[] = [];
  let depth = 0;
  let inStr = false;
  let start = 0;
  for (let i = 0; i < body.length; i++) {
    const c = body[i];
    if (inStr) {
      if (c === "\\") i++;
      else if (c === '"') inStr = false;
      continue;
    }
    if (c === '"') inStr = true;
    else if (c === "[" || c === "{") depth++;
    else if (c === "]" || c === "}") depth--;
    else if (c === "," && depth === 0) {
      out.push(body.slice(start, i).trim());
      start = i + 1;
    }
    if (depth < 0) return null;
  }
  if (inStr || depth !== 0) return null;
  const last = body.slice(start).trim();
  if (last || out.length) out.push(last);
  return out;
}

const VAR_ONLY = /^\{\{[^{}]+\}\}$/;

/** Reads a message's content as typed arguments; null when it isn't an argument array. */
export function parseArgs(content: string): Arg[] | null {
  if (!content.trim()) return [];
  const parts = splitTopLevel(content);
  if (!parts) return null;
  const args: Arg[] = [];
  for (const p of parts) {
    if (p === "") return null;
    if (p.startsWith('"')) {
      try {
        args.push({ type: "text", value: JSON.parse(p) as string });
        continue;
      } catch {
        return null;
      }
    }
    if (p === "true" || p === "false") args.push({ type: "boolean", value: p });
    else if (/^-?\d+(\.\d+)?([eE][+-]?\d+)?$/.test(p) || VAR_ONLY.test(p)) args.push({ type: "number", value: p });
    else if (p.startsWith("{") || p.startsWith("[") || p === "null") {
      const bin = /^\{\s*"\$binary"\s*:\s*"([^"]*)"\s*\}$/.exec(p);
      if (bin) args.push({ type: "binary", value: bin[1] });
      else args.push({ type: "json", value: p });
    } else return null;
  }
  return args;
}

/** Writes typed arguments back to the JSON array the engine sends. */
export function serializeArgs(args: Arg[]): string {
  const parts = args.map((a) => {
    switch (a.type) {
      case "text":
        return JSON.stringify(a.value);
      case "number":
        return a.value.trim() || "0";
      case "boolean":
        return a.value.trim() || "false";
      case "binary":
        return JSON.stringify({ $binary: a.value.trim() });
      default:
        return a.value.trim() || "null";
    }
  });
  return `[${parts.join(", ")}]`;
}

function jsonProblem(v: string): string | null {
  if (!v.trim() || /\{\{[^{}]+\}\}/.test(v)) return null;
  try {
    JSON.parse(v);
    return null;
  } catch (e) {
    return (e as Error).message;
  }
}

/** One Socket.IO message's arguments: a typed list, or the raw JSON array. */
export function ArgsEditor({ value, onChange, label = "Arguments" }: { value: string; onChange: (v: string) => void; label?: string }) {
  const parsed = parseArgs(value);
  const [raw, setRaw] = useState(parsed === null);
  const args = parsed ?? [];
  const setArgs = (next: Arg[]) => onChange(serializeArgs(next));
  const patch = (i: number, p: Partial<Arg>) => setArgs(args.map((a, j) => (j === i ? { ...a, ...p } : a)));
  const move = (i: number, dir: -1 | 1) => {
    const next = [...args];
    [next[i], next[i + dir]] = [next[i + dir], next[i]];
    setArgs(next);
  };

  return (
    <div className="args-editor">
      <div className="args-head">
        <span className="lt-label">
          {label}
          {!raw && args.length > 0 ? ` (${args.length})` : ""}
        </span>
        <label className="args-raw">
          <Switch size="small" checked={raw || parsed === null} disabled={parsed === null} onChange={setRaw} aria-label="Edit arguments as JSON" />
          <span className="muted small">JSON</span>
        </label>
      </div>
      {raw || parsed === null ? (
        <>
          <VarTextArea
            className="code-input mono"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            spellCheck={false}
            autoSize={{ minRows: 3, maxRows: 14 }}
            aria-label={`${label} as JSON`}
            placeholder='["hello", {"room": "{{room}}"}, 42]'
          />
          {parsed === null && value.trim() && <p className="muted small">Not an argument array, so it's sent as one text argument. Write a JSON array to use typed arguments.</p>}
        </>
      ) : (
        <>
          {args.length === 0 && <p className="muted small">No arguments: the event is emitted on its own.</p>}
          <ol className="args-list">
            {args.map((a, i) => {
              const problem = a.type === "json" ? jsonProblem(a.value) : null;
              return (
                <li key={i} className="arg-row">
                  <span className="arg-no">{i + 1}</span>
                  <Select
                    className="arg-type"
                    value={a.type}
                    onChange={(type) => patch(i, { type, value: convert(a, type) })}
                    options={TYPES}
                    aria-label={`Type of argument ${i + 1}`}
                  />
                  <div className="arg-value">
                    {a.type === "json" ? (
                      <VarTextArea
                        className="code-input mono"
                        value={a.value}
                        onChange={(e) => patch(i, { value: e.target.value })}
                        autoSize={{ minRows: 1, maxRows: 10 }}
                        spellCheck={false}
                        aria-label={`Argument ${i + 1}`}
                        placeholder='{"room": "{{room}}"}'
                        status={problem ? "warning" : undefined}
                      />
                    ) : a.type === "boolean" ? (
                      <Segmented
                        value={a.value === "false" ? "false" : a.value === "true" ? "true" : "var"}
                        onChange={(v) => patch(i, { value: v === "var" ? "{{flag}}" : String(v) })}
                        options={[
                          { value: "true", label: "true" },
                          { value: "false", label: "false" },
                          { value: "var", label: "{{variable}}" },
                        ]}
                        aria-label={`Argument ${i + 1}`}
                      />
                    ) : (
                      <VarInput
                        className={a.type === "text" ? undefined : "mono"}
                        value={a.value}
                        onChange={(e) => patch(i, { value: e.target.value })}
                        spellCheck={false}
                        aria-label={`Argument ${i + 1}`}
                        placeholder={a.type === "number" ? "42 or {{count}}" : a.type === "binary" ? "base64, e.g. AAEC/w==" : "text"}
                        status={a.type === "number" && a.value && !/^-?\d+(\.\d+)?([eE][+-]?\d+)?$/.test(a.value) && !VAR_ONLY.test(a.value) ? "warning" : undefined}
                      />
                    )}
                    {a.type === "boolean" && a.value !== "true" && a.value !== "false" && (
                      <VarInput className="mono arg-bool-var" value={a.value} onChange={(e) => patch(i, { value: e.target.value })} aria-label={`Variable for argument ${i + 1}`} />
                    )}
                    {problem && <span className="inline-warn">Not valid JSON yet: {problem}</span>}
                  </div>
                  <span className="arg-actions">
                    <Button type="text" icon={<ArrowUp size={13} />} disabled={i === 0} onClick={() => move(i, -1)} aria-label={`Move argument ${i + 1} up`} />
                    <Button type="text" icon={<ArrowDown size={13} />} disabled={i === args.length - 1} onClick={() => move(i, 1)} aria-label={`Move argument ${i + 1} down`} />
                    <Button type="text" icon={<X size={13} />} onClick={() => setArgs(args.filter((_, j) => j !== i))} aria-label={`Remove argument ${i + 1}`} />
                  </span>
                </li>
              );
            })}
          </ol>
          <div>
            <Button icon={<Plus size={13} />} onClick={() => setArgs([...args, { type: "text", value: "" }])}>
              Add argument
            </Button>
          </div>
        </>
      )}
    </div>
  );
}

/** Keeps what it can of a value when its type changes. */
function convert(a: Arg, to: ArgType): string {
  if (a.type === to) return a.value;
  if (to === "boolean") return a.value === "true" ? "true" : "false";
  if (to === "number") return /^-?\d+(\.\d+)?$/.test(a.value) || VAR_ONLY.test(a.value) ? a.value : "0";
  if (to === "json") return a.type === "text" && a.value ? JSON.stringify(a.value) : a.value || "{}";
  if (a.type === "json" && to === "text") {
    try {
      const v = JSON.parse(a.value);
      if (typeof v === "string") return v;
    } catch {
      /* keep as written */
    }
  }
  return a.value;
}

export type WsFormat = "text" | "json" | "binary";

/** A WebSocket frame carries one payload; Postman's formats are text, JSON and binary. */
export function WsPayloadEditor({
  value,
  binary,
  onChange,
  label = "Message",
}: {
  value: string;
  binary: boolean;
  onChange: (value: string, binary: boolean) => void;
  label?: string;
}) {
  const [format, setFormat] = useState<WsFormat>(binary ? "binary" : /^\s*[[{]/.test(value) ? "json" : "text");
  const shown: WsFormat = binary ? "binary" : format === "binary" ? "text" : format;
  const problem = shown === "json" ? jsonProblem(value) : null;
  return (
    <div className="args-editor">
      <div className="args-head">
        <Segmented
          size="small"
          value={shown}
          onChange={(f) => {
            setFormat(f as WsFormat);
            onChange(value, f === "binary");
          }}
          options={[
            { value: "text", label: "Text" },
            { value: "json", label: "JSON" },
            { value: "binary", label: "Binary (base64)" },
          ]}
          aria-label="Message format"
        />
        {shown === "json" && value.trim() && !problem && (
          <Button onClick={() => onChange(JSON.stringify(JSON.parse(value), null, 2), false)}>Format</Button>
        )}
      </div>
      <VarTextArea
        className="code-input mono"
        value={value}
        onChange={(e) => onChange(e.target.value, shown === "binary")}
        spellCheck={false}
        autoSize={{ minRows: 3, maxRows: 14 }}
        aria-label={label}
        placeholder={shown === "binary" ? "AAEC/w==" : shown === "json" ? '{"op": "subscribe", "channel": "{{channel}}"}' : "hello"}
        status={problem ? "warning" : undefined}
      />
      {problem && <Alert type="warning" showIcon title={`Not valid JSON yet: ${problem}. It will be sent as written.`} />}
    </div>
  );
}
