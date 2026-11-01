import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FileInput, Upload } from "lucide-react";
import { api, type TreeNode } from "../api";
import { useApp } from "../store";

// One place for every import: files (Postman, OpenAPI/Swagger, HAR, cURL)
// and pasted text, into a new collection or an existing one.

const FORMATS = "Postman collections and environments, OpenAPI 3 and Swagger 2 (JSON or YAML), HAR recordings and cURL commands";

export function ImportPage() {
  const qc = useQueryClient();
  const setReport = useApp((s) => s.setReport);
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const fileRef = useRef<HTMLInputElement>(null);
  const [text, setText] = useState("");
  const [into, setInto] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);

  const targets: { path: string; label: string }[] = [];
  const walk = (n: TreeNode, depth: number) => {
    if (n.type === "request") return;
    targets.push({ path: n.path, label: `${"  ".repeat(depth)}${n.name}` });
    if (depth < 2) n.children?.forEach((c) => walk(c, depth + 1));
  };
  ws.data?.collections.forEach((c) => walk(c, 0));

  const run = async (items: { text: string; filename?: string }[]) => {
    setBusy(true);
    setError(null);
    try {
      let last = null;
      for (const it of items) {
        try {
          last = await api.importFile(it.text, { filename: it.filename, into: into || undefined });
        } catch (e) {
          throw new Error(`${it.filename ?? "Pasted text"}: ${(e as Error).message}`);
        }
      }
      await Promise.all([qc.invalidateQueries({ queryKey: ["workspace"] }), qc.invalidateQueries({ queryKey: ["environments"] })]);
      if (last) setReport({ mode: "import", data: last });
      setText("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };
  const fromFiles = async (files: FileList | null) => {
    if (!files?.length) return;
    run(await Promise.all([...files].map(async (f) => ({ text: await f.text(), filename: f.name }))));
  };

  return (
    <section className="import-page">
      <header className="lt-head">
        <div>
          <h1>Import</h1>
          <p className="muted small">{FORMATS}. The format is detected.</p>
        </div>
      </header>

      <label className="field-row">
        <span className="field-label">Add to</span>
        <select value={into} onChange={(e) => setInto(e.target.value)} aria-label="Add to">
          <option value="">A new collection</option>
          {targets.map((t) => (
            <option key={t.path} value={t.path}>
              {t.label}
            </option>
          ))}
        </select>
      </label>

      <div
        className={`drop-zone ${dragging ? "is-over" : ""}`}
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragging(false);
          fromFiles(e.dataTransfer.files);
        }}
      >
        <Upload size={18} aria-hidden />
        <span>Drop files here, or</span>
        <button className="btn ghost" onClick={() => fileRef.current?.click()} disabled={busy}>
          <FileInput size={14} /> Choose files
        </button>
        <input ref={fileRef} type="file" multiple hidden accept=".json,.yaml,.yml,.har,.txt,.sh,.curl" onChange={(e) => fromFiles(e.target.files)} />
      </div>

      <label className="paste">
        <span className="field-label">Or paste</span>
        <textarea
          className="code-input mono"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={"curl 'https://api.example.com/orders' -H 'accept: application/json'"}
          rows={8}
          spellCheck={false}
        />
      </label>
      <div className="confirm-actions">
        <button className="btn primary" disabled={busy || !text.trim()} onClick={() => run([{ text }])}>
          {busy ? "Importing…" : "Import"}
        </button>
      </div>
      {error && <p className="inline-error">{error}</p>}
    </section>
  );
}
