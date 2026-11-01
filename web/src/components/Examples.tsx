import { useState } from "react";
import { ChevronRight, Trash2 } from "lucide-react";
import type { Example } from "../api";

// Saved responses for a request: documentation you can share through the
// workspace (and Postman exports). "Save as example" on a response adds one.

export function ExamplesEditor({ examples, onChange }: { examples: Example[]; onChange: (e: Example[]) => void }) {
  const [open, setOpen] = useState<number | null>(examples.length === 1 ? 0 : null);
  if (examples.length === 0) {
    return <p className="muted">No examples yet. Send the request and use <strong>Save as example</strong> on the response to keep one here.</p>;
  }
  const update = (i: number, patch: Partial<Example>) => onChange(examples.map((e, j) => (j === i ? { ...e, ...patch } : e)));
  return (
    <ul className="examples">
      {examples.map((ex, i) => (
        <li key={i} className="example">
          <div className="example-head">
            <button className="icon-btn" onClick={() => setOpen(open === i ? null : i)} aria-expanded={open === i} aria-label={`Show example ${ex.name}`}>
              <ChevronRight size={14} className={`chev ${open === i ? "open" : ""}`} />
            </button>
            {ex.status ? <span className={`status s${String(ex.status)[0]}`}>{ex.status}</span> : null}
            <input className="cell" value={ex.name} onChange={(e) => update(i, { name: e.target.value })} aria-label="Example name" />
            <button className="icon-btn" onClick={() => onChange(examples.filter((_, j) => j !== i))} aria-label={`Delete example ${ex.name}`}>
              <Trash2 size={13} />
            </button>
          </div>
          {open === i && (
            <div className="example-body">
              {(ex.headers?.length ?? 0) > 0 && (
                <table className="data-table">
                  <tbody>
                    {ex.headers!.map((h, k) => (
                      <tr key={k}>
                        <td className="mono key">{h.key}</td>
                        <td className="mono">{h.value}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <textarea className="code-input mono" value={ex.body ?? ""} onChange={(e) => update(i, { body: e.target.value })} spellCheck={false} aria-label="Example body" />
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}
