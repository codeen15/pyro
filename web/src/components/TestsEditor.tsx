import { Button, Input, Select } from "antd";
import { Plus, X } from "../icons";
import type { Assertion, Extractor, PyroRequest } from "../api";

const ASSERT_TYPES = [
  { value: "status", label: "Status code", needsPath: false },
  { value: "header", label: "Header", needsPath: true, placeholder: "Content-Type" },
  { value: "jsonpath", label: "JSON path", needsPath: true, placeholder: "$.data.id" },
  { value: "body", label: "Body text", needsPath: false },
  { value: "duration", label: "Response time (ms)", needsPath: false },
];

const OPS = [
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

const EXTRACT_FROM = [
  { value: "jsonpath", label: "JSON path", placeholder: "$.token" },
  { value: "header", label: "Header", placeholder: "X-Request-Id" },
  { value: "regex", label: "Regex (1st group)", placeholder: "id=(\\d+)" },
  { value: "status", label: "Status code", placeholder: "" },
];

export function TestsEditor({
  assertions,
  extract,
  onAssertions,
  onExtract,
}: {
  assertions: Assertion[];
  extract: Extractor[];
  onAssertions: (a: Assertion[] | undefined) => void;
  onExtract: (e: Extractor[] | undefined) => void;
}) {
  const setA = (i: number, patch: Partial<Assertion>) => onAssertions(assertions.map((a, j) => (j === i ? { ...a, ...patch } : a)));
  const setE = (i: number, patch: Partial<Extractor>) => onExtract(extract.map((e, j) => (j === i ? { ...e, ...patch } : e)));
  const orUndef = <T,>(list: T[]) => (list.length ? list : undefined);

  return (
    <div className="tests-editor">
      <section>
        <h3 className="sub-head">Assertions</h3>
        <p className="muted small">Checked after every send and in collection runs. Faster than scripts under load.</p>
        {assertions.length > 0 && (
          <table className="rule-table tests-rules">
            <tbody>
              {assertions.map((a, i) => {
                const t = ASSERT_TYPES.find((x) => x.value === a.type) ?? ASSERT_TYPES[0];
                const op = a.op || (a.value ? "eq" : t.needsPath ? "exists" : "eq");
                const noValue = op === "exists" || op === "notexists";
                return (
                  <tr key={i}>
                    <td>
                      <Select
                        value={a.type}
                        onChange={(v) => setA(i, { type: v, path: undefined })}
                        aria-label="Check"
                        popupMatchSelectWidth={false}
                        options={ASSERT_TYPES.map((x) => ({ value: x.value, label: x.label }))}
                      />
                    </td>
                    <td>
                      {t.needsPath && (
                        <Input className="mono" value={a.path ?? ""} placeholder={t.placeholder} onChange={(e) => setA(i, { path: e.target.value })} aria-label="Path" spellCheck={false} />
                      )}
                    </td>
                    <td>
                      <Select value={op} onChange={(v) => setA(i, { op: v })} aria-label="Operator" popupMatchSelectWidth={false} options={OPS} />
                    </td>
                    <td>
                      {!noValue && (
                        <Input className="mono" value={a.value ?? ""} onChange={(e) => setA(i, { value: e.target.value })} aria-label="Expected value" spellCheck={false} />
                      )}
                    </td>
                    <td className="kv-actions">
                      <Button type="text" icon={<X size={14} />} onClick={() => onAssertions(orUndef(assertions.filter((_, j) => j !== i)))} aria-label="Remove assertion" title="Remove" />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <Button type="dashed" icon={<Plus size={13} />} onClick={() => onAssertions([...assertions, { type: "status", op: "eq", value: "200" }])}>
          Add assertion
        </Button>
      </section>

      <section>
        <h3 className="sub-head">Extract into variables</h3>
        <p className="muted small">
          Saves part of the response as a variable, e.g. a login token for the next request. Use it as <code>{"{{name}}"}</code>.
        </p>
        {extract.length > 0 && (
          <table className="rule-table tests-rules">
            <tbody>
              {extract.map((x, i) => {
                const src = EXTRACT_FROM.find((f) => f.value === x.from) ?? EXTRACT_FROM[0];
                return (
                  <tr key={i}>
                    <td>
                      <Input className="mono" value={x.var} placeholder="variable" onChange={(e) => setE(i, { var: e.target.value })} aria-label="Variable name" spellCheck={false} />
                    </td>
                    <td>
                      <Select
                        value={x.from}
                        onChange={(v) => setE(i, { from: v })}
                        aria-label="From"
                        popupMatchSelectWidth={false}
                        options={EXTRACT_FROM.map((f) => ({ value: f.value, label: f.label }))}
                      />
                    </td>
                    <td colSpan={2}>
                      {x.from !== "status" && (
                        <Input className="mono" value={x.path ?? ""} placeholder={src.placeholder} onChange={(e) => setE(i, { path: e.target.value })} aria-label="Path" spellCheck={false} />
                      )}
                    </td>
                    <td className="kv-actions">
                      <Button type="text" icon={<X size={14} />} onClick={() => onExtract(orUndef(extract.filter((_, j) => j !== i)))} aria-label="Remove extractor" title="Remove" />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <Button type="dashed" icon={<Plus size={13} />} onClick={() => onExtract([...extract, { var: "", from: "jsonpath", path: "" }])}>
          Add extractor
        </Button>
      </section>
    </div>
  );
}

export function ScriptsEditor({ scripts, onChange }: { scripts: PyroRequest["scripts"]; onChange: (s: PyroRequest["scripts"]) => void }) {
  const set = (k: "pre" | "post", v: string) => {
    const next = { ...scripts, [k]: v };
    onChange(next.pre?.trim() || next.post?.trim() ? next : undefined);
  };
  return (
    <div className="split-2 scripts-editor">
      <label>
        <span className="field-label">Pre-request</span>
        <Input.TextArea
          className="code-input"
          autoSize={{ minRows: 8 }}
          value={scripts?.pre ?? ""}
          onChange={(e) => set("pre", e.target.value)}
          spellCheck={false}
          placeholder={'pm.variables.set("ts", Date.now());'}
        />
      </label>
      <label>
        <span className="field-label">Post-response</span>
        <Input.TextArea
          className="code-input"
          autoSize={{ minRows: 8 }}
          value={scripts?.post ?? ""}
          onChange={(e) => set("post", e.target.value)}
          spellCheck={false}
          placeholder={'pm.test("status is 200", () => {\n  pm.response.to.have.status(200);\n});'}
        />
      </label>
      <p className="muted small span-2">
        JavaScript with Postman's <code>pm.*</code> API (<code>pm.test</code>, <code>pm.expect</code>, <code>pm.environment</code>,{" "}
        <code>pm.sendRequest</code>…). Collection and folder scripts run first.
      </p>
    </div>
  );
}
