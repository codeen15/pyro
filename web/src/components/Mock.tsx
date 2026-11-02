import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Badge, Button, Empty, InputNumber, Skeleton, Table, Tooltip } from "antd";
import { Copy, Play, Square } from "../icons";
import { api } from "../api";
import { useApp } from "../store";

// A collection's mock server: its saved examples answered on a local port.

export function MockPanel({ slug }: { slug: string }) {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const [port, setPort] = useState<number | null>(null);
  const [delay, setDelay] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ["mock", slug],
    queryFn: async () => {
      const v = await api.mock(slug);
      return v.running ? v : null;
    },
    refetchInterval: (query) => (query.state.data ? 2000 : false),
  });
  const m = q.data;

  const start = async () => {
    setError(null);
    try {
      await api.startMock(slug, { port: port || undefined, delayMs: delay || undefined });
      qc.invalidateQueries({ queryKey: ["mock", slug] });
    } catch (e) {
      setError((e as Error).message);
    }
  };
  const stop = async () => {
    await api.stopMock(slug);
    qc.setQueryData(["mock", slug], null);
  };

  if (q.isPending) return <Skeleton active paragraph={{ rows: 3 }} />;
  if (!m) {
    return (
      <div className="mock-panel">
        <p className="muted">
          Serves this collection's saved examples as an API on this machine: each request with examples becomes a route. Pick an example with the{" "}
          <code>X-Mock-Example</code> (name) or <code>X-Mock-Status</code> (code) header; otherwise the first 2xx example answers.
        </p>
        <div className="mock-start">
          <label className="field-row">
            <span className="field-label">Port</span>
            <InputNumber className="mono" value={port} onChange={setPort} min={1} max={65535} precision={0} controls={false} placeholder="any free port" aria-label="Port" />
          </label>
          <label className="field-row">
            <span className="field-label">Delay (ms)</span>
            <InputNumber className="mono" value={delay} onChange={setDelay} min={0} precision={0} controls={false} placeholder="0" aria-label="Delay" />
          </label>
          <Button type="primary" icon={<Play size={14} />} onClick={start}>
            Start mock server
          </Button>
        </div>
        {error && <Alert type="error" showIcon title={error} />}
      </div>
    );
  }
  const hits = [...(m.hits ?? [])].reverse().slice(0, 50).map((h, i) => ({ ...h, key: i }));
  return (
    <div className="mock-panel">
      <div className="mock-running">
        <Badge status="processing" text="Running" />
        <code className="mono">{m.url}</code>
        <Tooltip title="Copy URL">
          <Button type="text" size="small" icon={<Copy size={13} />} onClick={() => navigator.clipboard?.writeText(m.url)} aria-label="Copy mock URL" />
        </Tooltip>
        <Button size="small" icon={<Square size={13} />} onClick={stop}>
          Stop
        </Button>
      </div>
      <h2 className="mock-head">Routes</h2>
      {(m.routes?.length ?? 0) === 0 ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="No routes yet: save examples on this collection's requests (Save as example on a response). New ones are picked up within a second."
        />
      ) : (
        <Table
          size="small"
          bordered
          pagination={false}
          scroll={{ x: "max-content" }}
          rowKey={(r) => r.method + r.path}
          dataSource={m.routes}
          columns={[
            { title: "Method", dataIndex: "method", key: "method", className: "mono" },
            { title: "Path", dataIndex: "path", key: "path", className: "mono" },
            {
              title: "Request",
              key: "request",
              render: (_, r) => (
                <Button type="link" size="small" className="inline-link" onClick={() => select(`${slug}/${r.request}`)}>
                  {r.name}
                </Button>
              ),
            },
            { title: "Examples", key: "examples", className: "muted small", render: (_, r) => r.examples.map((e) => e.name).join(", ") },
          ]}
        />
      )}
      <h2 className="mock-head">Recent requests</h2>
      {hits.length === 0 ? (
        <p className="muted">
          None yet. Try{" "}
          <code className="mono">
            curl {m.url}
            {m.routes?.[0]?.path.replace(/\{[^}]+\}/g, "1") ?? "/"}
          </code>
        </p>
      ) : (
        <Table
          size="small"
          bordered
          pagination={false}
          scroll={{ x: "max-content" }}
          dataSource={hits}
          columns={[
            { title: "Time", key: "at", className: "mono muted", render: (_, h) => new Date(h.at).toLocaleTimeString() },
            { title: "Status", dataIndex: "status", key: "status", className: "num mono" },
            { title: "Request", key: "req", className: "mono", render: (_, h) => `${h.method} ${h.path}` },
            { title: "Example", key: "example", className: "muted small", render: (_, h) => h.example ?? "no match" },
          ]}
        />
      )}
    </div>
  );
}
