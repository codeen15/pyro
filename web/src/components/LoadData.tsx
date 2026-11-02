import { useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Checkbox, Dropdown, Input, Select, Table } from "antd";
import { isMap, isSeq, type Document, type YAMLMap } from "yaml";
import { api, type DataFormat, type SavedMessage } from "../api";
import { ArgsEditor, WsPayloadEditor } from "./MessageArgs";
import { Plus, Trash2, Upload, X } from "../icons";

const MODES = [
  { value: "shared", label: "Shared: next row each time" },
  { value: "per-vu", label: "Per VU: one row each" },
  { value: "random", label: "Random row" },
];
const ON_END = [
  { value: "recycle", label: "Start over" },
  { value: "stop", label: "Stop the VU" },
];
const DELIMITERS = [
  { value: ",", label: "Comma ," },
  { value: ";", label: "Semicolon ;" },
  { value: "tab", label: "Tab" },
  { value: "|", label: "Pipe |" },
];

/** A scenario's test data: which file, how rows are handed out, and how its columns become {{variables}}. */
export function DataEditor({ doc, base, update }: { doc: Document; base: string[]; update: (fn: (d: Document) => void) => void }) {
  const qc = useQueryClient();
  const { message } = App.useApp();
  const fileInput = useRef<HTMLInputElement>(null);
  const path = [...base, "data"];
  const node = doc.getIn(path) as YAMLMap | undefined;
  const files = useQuery({ queryKey: ["data-files"], queryFn: api.dataFiles });

  const data = isMap(node) ? (node.toJSON() as { file?: string; mode?: string; onEnd?: string } & DataFormat) : undefined;
  const set = (k: string, v: unknown) =>
    update((d) => {
      if (v === undefined || v === "" || (Array.isArray(v) && v.length === 0)) d.deleteIn([...path, k]);
      else d.setIn([...path, k], d.createNode(v));
    });

  const fmt: DataFormat = data ? { header: data.header, variables: data.variables, map: data.map, delimiter: data.delimiter } : {};
  const preview = useQuery({
    queryKey: ["data-preview", data?.file, fmt],
    queryFn: () => api.previewData({ file: data!.file!, ...fmt }),
    // Only for files that exist: a missing one would just 404.
    enabled: !!data?.file && !!files.data?.some((f) => f.path === data.file),
    retry: false,
  });

  const upload = async (f: File) => {
    try {
      const r = await api.uploadData(f.name, await f.text());
      await qc.invalidateQueries({ queryKey: ["data-files"] });
      await qc.invalidateQueries({ queryKey: ["data-preview"] });
      if (data) set("file", r.path);
      else update((d) => d.setIn(path, d.createNode({ file: r.path, mode: "shared" })));
      message.success(`Saved ${r.path}`);
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const picker = (
    <input
      ref={fileInput}
      type="file"
      accept=".csv,.tsv,.txt,.json,text/csv"
      hidden
      onChange={(e) => {
        const f = e.target.files?.[0];
        e.target.value = "";
        if (f) upload(f);
      }}
    />
  );

  if (!data) {
    return (
      <div className="lt-data">
        <h3 className="sub-head">Test data</h3>
        <p className="muted small">Feed each iteration a row from a CSV or JSON file (users, products…). Its columns become {"{{variables}}"} in requests.</p>
        <div className="lt-data-actions">
          <Button icon={<Upload size={14} />} onClick={() => fileInput.current?.click()}>
            Upload CSV
          </Button>
          {(files.data?.length ?? 0) > 0 && (
            <Select
              className="lt-data-file"
              placeholder="Use a file in data/"
              aria-label="Data file"
              options={files.data!.map((f) => ({ value: f.path, label: f.path }))}
              onChange={(file) => update((d) => d.setIn(path, d.createNode({ file, mode: "shared" })))}
            />
          )}
        </div>
        {picker}
      </div>
    );
  }

  const vars = data.variables ?? [];
  const headerChecked = vars.length > 0 ? data.header === true : data.header !== false;
  const setHeader = (on: boolean) => {
    if (vars.length > 0) set("header", on ? true : undefined);
    else set("header", on ? undefined : false);
  };
  const setVariable = (col: string, v: string) => {
    const map = { ...(data.map ?? {}) };
    if (v.trim() === col) delete map[col];
    else map[col] = v.trim();
    set("map", Object.keys(map).length ? map : undefined);
  };
  const p = preview.data;
  const known = files.data?.some((f) => f.path === data.file);

  return (
    <div className="lt-data">
      <div className="lt-data-head">
        <h3 className="sub-head">Test data</h3>
        <Button type="text" icon={<Trash2 size={14} />} aria-label="Remove test data" onClick={() => update((d) => d.deleteIn(path))} />
      </div>
      <div className="lt-fields">
        <label className="lt-field lt-field-wide">
          <span className="lt-label">File</span>
          <div className="lt-data-filerow">
            <Select
              className="mono"
              value={data.file}
              aria-label="Data file"
              onChange={(f) => set("file", f)}
              options={[
                ...(known || !data.file ? [] : [{ value: data.file, label: `${data.file} (not found)` }]),
                ...(files.data ?? []).map((f) => ({ value: f.path, label: f.path })),
              ]}
            />
            <Button icon={<Upload size={14} />} onClick={() => fileInput.current?.click()} aria-label="Upload a data file">
              Upload
            </Button>
          </div>
        </label>
        <label className="lt-field">
          <span className="lt-label">Rows</span>
          <Select value={data.mode ?? "shared"} onChange={(v) => set("mode", v)} options={MODES} aria-label="How rows are handed out" popupMatchSelectWidth={false} />
        </label>
        <label className="lt-field">
          <span className="lt-label">When rows run out</span>
          <Select value={data.onEnd ?? "recycle"} onChange={(v) => set("onEnd", v === "recycle" ? undefined : v)} options={ON_END} aria-label="When rows run out" />
        </label>
      </div>
      <div className="lt-fields">
        <label className="lt-field lt-field-wide">
          <span className="lt-label">Variable names (JMeter style, optional)</span>
          <Input
            className="mono"
            placeholder="user,password,region — leave empty to use the first line"
            value={vars.join(",")}
            onChange={(e) => {
              const list = e.target.value.split(",").map((s) => s.trim());
              set("variables", list.some(Boolean) ? list : undefined);
              if (!list.some(Boolean) && data.header === true) set("header", undefined);
            }}
            aria-label="Variable names"
          />
        </label>
        <label className="lt-field">
          <span className="lt-label">Delimiter</span>
          <Select value={data.delimiter ?? ","} onChange={(v) => set("delimiter", v === "," ? undefined : v)} options={DELIMITERS} aria-label="Delimiter" />
        </label>
        <div className="lt-field lt-data-check">
          <Checkbox checked={headerChecked} onChange={(e) => setHeader(e.target.checked)}>
            {vars.length > 0 ? "Skip the first line (it's a header)" : "First line has the column names"}
          </Checkbox>
        </div>
      </div>

      {files.data && data.file && !known && (
        <Alert type="warning" showIcon title={`${data.file} isn't in the workspace yet. Upload it (or copy it into data/) before running.`} />
      )}
      {preview.error && <Alert type="warning" showIcon title={(preview.error as Error).message} />}
      {p && (
        <>
          <p className="muted small">
            {p.total} row{p.total === 1 ? "" : "s"}. Rename a column's variable below (empty leaves it out); use them in requests as{" "}
            {p.variables
              .filter(Boolean)
              .slice(0, 4)
              .map((v, i) => (
                <code key={v}>
                  {i > 0 ? " " : ""}
                  {`{{${v}}}`}
                </code>
              ))}
            .
          </p>
          <div className="lt-data-preview">
            <Table
              size="small"
              bordered
              pagination={false}
              scroll={{ x: "max-content" }}
              rowKey={(_, i) => String(i)}
              dataSource={p.rows}
              columns={p.columns.map((col, i) => ({
                key: col + i,
                title: (
                  <div className="lt-data-col">
                    {col !== p.variables[i] ? <span className="muted small mono">{col}</span> : null}
                    <Input
                      size="small"
                      className="mono"
                      value={p.variables[i]}
                      placeholder="(left out)"
                      onChange={(e) => setVariable(col, e.target.value)}
                      aria-label={`Variable for column ${col}`}
                    />
                  </div>
                ),
                render: (_: unknown, row: Record<string, string>) => <span className="mono">{p.variables[i] ? row[p.variables[i]] : ""}</span>,
              }))}
            />
          </div>
        </>
      )}
      {picker}
    </div>
  );
}

/** For a WebSocket or Socket.IO step: how long to keep the connection open and what to send. */
export function StreamStepEditor({
  doc,
  path,
  update,
  request,
  kind,
}: {
  doc: Document;
  path: (string | number)[];
  update: (fn: (d: Document) => void) => void;
  request: string;
  kind: "websocket" | "socketio";
}) {
  const sio = kind === "socketio";
  const saved = useQuery({ queryKey: ["request", request], queryFn: () => api.request(request), enabled: !!request, retry: false });
  const sendNode = doc.getIn([...path, "send"]);
  const msgs: SavedMessage[] = isSeq(sendNode) ? (sendNode.toJSON() as SavedMessage[]) : [];
  const hold = doc.getIn([...path, "hold"]);
  const setMsgs = (list: SavedMessage[]) =>
    update((d) => {
      if (list.length) d.setIn([...path, "send"], d.createNode(list));
      else d.deleteIn([...path, "send"]);
    });
  const patch = (i: number, p: Partial<SavedMessage>) => setMsgs(msgs.map((m, j) => (j === i ? clean({ ...m, ...p }) : m)));
  const templates = saved.data?.messages ?? [];

  return (
    <div className="step-stream">
      <label className="step-hold">
        <span className="lt-label">Keep open</span>
        <Input
          className="mono"
          value={hold === undefined ? "" : String(hold)}
          placeholder="0s"
          onChange={(e) => update((d) => (e.target.value.trim() ? d.setIn([...path, "hold"], e.target.value.trim()) : d.deleteIn([...path, "hold"])))}
          aria-label="Keep the connection open for"
        />
      </label>
      <div className="step-msgs">
        <span className="lt-label">{sio ? "Events to emit" : "Messages to send"} after connecting, in order</span>
        {msgs.map((m, i) => (
          <div key={i} className="step-msg">
            {sio && (
              <Input
                className="mono step-msg-event"
                value={m.event ?? ""}
                placeholder="event"
                onChange={(e) => patch(i, { event: e.target.value })}
                aria-label={`Event of message ${i + 1}`}
              />
            )}
            <div className="step-msg-args">
              {sio ? (
                <ArgsEditor value={m.content} onChange={(content) => patch(i, { content })} label={`Arguments of message ${i + 1}`} />
              ) : (
                <WsPayloadEditor value={m.content} binary={!!m.binary} onChange={(content, binary) => patch(i, { content, binary })} label={`Payload of message ${i + 1}`} />
              )}
            </div>
            {sio ? (
              <Checkbox checked={!!m.ack} onChange={(e) => patch(i, { ack: e.target.checked })}>
                Ack
              </Checkbox>
            ) : (
              <span />
            )}
            <Button type="text" icon={<X size={14} />} onClick={() => setMsgs(msgs.filter((_, j) => j !== i))} aria-label={`Remove message ${i + 1}`} />
          </div>
        ))}
        <div className="step-msg-add">
          <Button icon={<Plus size={13} />} onClick={() => setMsgs([...msgs, sio ? { event: "message", content: "[]" } : { content: "" }])}>
            {sio ? "Event" : "Message"}
          </Button>
          {templates.length > 0 && (
            <Dropdown
              trigger={["click"]}
              menu={{
                items: templates.map((t, i) => ({ key: String(i), label: t.name || t.event || t.content.slice(0, 30) || `Message ${i + 1}` })),
                onClick: ({ key }) => setMsgs([...msgs, clean({ ...templates[Number(key)], name: undefined })]),
              }}
            >
              <Button>From the request's saved messages</Button>
            </Dropdown>
          )}
        </div>
        <span className="muted small">{"{{variables}}"} (including test data columns) are filled in per VU. Sent messages, acks and round trips are measured.</span>
      </div>
    </div>
  );
}

function clean(m: SavedMessage): SavedMessage {
  const out: SavedMessage = { content: m.content ?? "" };
  if (m.event) out.event = m.event;
  if (m.ack) out.ack = true;
  if (m.binary) out.binary = true;
  if (m.name) out.name = m.name;
  return out;
}
