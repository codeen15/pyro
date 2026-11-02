import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Input, Select, Upload } from "antd";
import { Upload as UploadIcon } from "../icons";
import { api, type TreeNode } from "../api";
import { useApp } from "../store";

// One place for every import: files (Postman, OpenAPI/Swagger, HAR, cURL)
// and pasted text, into a new collection or an existing one.

const FORMATS = "Postman collections and environments, OpenAPI 3 and Swagger 2 (JSON or YAML), HAR recordings, JMeter test plans (.jmx, as requests plus a load test) and cURL commands";

export function ImportPage() {
  const qc = useQueryClient();
  const setReport = useApp((s) => s.setReport);
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const [text, setText] = useState("");
  const [into, setInto] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const targets: { value: string; label: string; depth: number }[] = [{ value: "", label: "A new collection", depth: 0 }];
  const walk = (n: TreeNode, depth: number) => {
    if (n.type === "request") return;
    targets.push({ value: n.path, label: n.name, depth });
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
    }
  };
  const fromFiles = async (files: File[]) => {
    if (!files.length) return;
    run(await Promise.all(files.map(async (f) => ({ text: await f.text(), filename: f.name }))));
  };

  return (
    <div className="page">
      <section className="card import-card">
        <header className="page-head">
          <div>
            <h1>Import</h1>
            <p className="muted small">{FORMATS}. The format is detected.</p>
          </div>
        </header>

        <div className="field-row">
          <span className="field-label">Add to</span>
          <Select
            className="wide"
            aria-label="Add to"
            value={into}
            onChange={setInto}
            options={targets}
            optionRender={(o) => <span style={{ paddingLeft: (o.data.depth ?? 0) * 16 }}>{o.data.label}</span>}
            showSearch={{ optionFilterProp: "label" }}
          />
        </div>

        <Upload.Dragger
          className="drop-zone"
          multiple
          disabled={busy}
          showUploadList={false}
          accept=".json,.yaml,.yml,.har,.jmx,.txt,.sh,.curl"
          beforeUpload={(file, list) => {
            // Called once per file with the whole selection: import them together, once.
            if (file === list[0]) fromFiles(list);
            return false;
          }}
        >
          <p className="ant-upload-drag-icon">
            <UploadIcon size={32} />
          </p>
          <p className="ant-upload-text">Drop files here, or click to choose files</p>
          <p className="ant-upload-hint">JSON, YAML, HAR, JMX or a cURL command in a text file</p>
        </Upload.Dragger>

        <label className="paste">
          <span className="field-label">Or paste</span>
          <Input.TextArea
            className="code-input"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={"curl 'https://api.example.com/orders' -H 'accept: application/json'"}
            rows={8}
            spellCheck={false}
          />
        </label>
        <div className="import-actions">
          <Button type="primary" disabled={!text.trim()} loading={busy} onClick={() => run([{ text }])}>
            {busy ? "Importing" : "Import"}
          </Button>
        </div>
        {error && <Alert type="error" showIcon title={error} />}
      </section>
    </div>
  );
}
