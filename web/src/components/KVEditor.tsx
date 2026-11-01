import { FileUp, X } from "lucide-react";
import type { KeyValue } from "../api";

interface Props {
  rows: KeyValue[];
  onChange: (rows: KeyValue[]) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  /** Show a per-row toggle that turns the value into a file path (multipart). */
  allowFiles?: boolean;
  label: string;
}

/** Editable key/value table. A trailing blank row becomes a real row on first input. */
export function KVEditor({ rows, onChange, keyPlaceholder = "Key", valuePlaceholder = "Value", allowFiles, label }: Props) {
  const all = [...rows, { key: "", value: "" }];
  const update = (i: number, patch: Partial<KeyValue>) => {
    const next = all.map((r, j) => (j === i ? { ...r, ...patch } : r));
    onChange(next.filter((r, j) => j < rows.length || r.key !== "" || r.value !== ""));
  };
  const remove = (i: number) => onChange(rows.filter((_, j) => j !== i));

  return (
    <table className="kv-edit" aria-label={label}>
      <tbody>
        {all.map((r, i) => {
          const blank = i === rows.length;
          return (
            <tr key={i} className={r.disabled ? "is-off" : undefined}>
              <td className="kv-check">
                {!blank && (
                  <input
                    type="checkbox"
                    checked={!r.disabled}
                    onChange={(e) => update(i, { disabled: !e.target.checked })}
                    aria-label={`Include ${r.key || "row"}`}
                  />
                )}
              </td>
              <td>
                <input
                  className="cell mono"
                  value={r.key}
                  placeholder={keyPlaceholder}
                  onChange={(e) => update(i, { key: e.target.value })}
                  aria-label={`${label} key ${i + 1}`}
                  spellCheck={false}
                />
              </td>
              <td>
                <input
                  className="cell mono"
                  value={r.value}
                  placeholder={r.file ? "path/to/file" : valuePlaceholder}
                  onChange={(e) => update(i, { value: e.target.value })}
                  aria-label={`${label} value ${i + 1}`}
                  spellCheck={false}
                />
              </td>
              <td className="kv-actions">
                {!blank && allowFiles && (
                  <button
                    className={`icon-btn ${r.file ? "is-on" : ""}`}
                    onClick={() => update(i, { file: !r.file })}
                    aria-pressed={!!r.file}
                    title={r.file ? "Value is a file path" : "Send value as text"}
                    aria-label="Toggle file field"
                  >
                    <FileUp size={14} />
                  </button>
                )}
                {!blank && (
                  <button className="icon-btn" onClick={() => remove(i)} aria-label={`Remove ${r.key || "row"}`} title="Remove">
                    <X size={14} />
                  </button>
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
