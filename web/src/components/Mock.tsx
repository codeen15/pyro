import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Play, Square } from "lucide-react";
import { api } from "../api";
import { useApp } from "../store";

// A collection's mock server: its saved examples answered on a local port.

export function MockPanel({ slug }: { slug: string }) {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const [port, setPort] = useState("");
  const [delay, setDelay] = useState("");
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
      await api.startMock(slug, { port: Number(port) || undefined, delayMs: Number(delay) || undefined });
      qc.invalidateQueries({ queryKey: ["mock", slug] });
    } catch (e) {
      setError((e as Error).message);
    }
  };
  const stop = async () => {
    await api.stopMock(slug);
    qc.setQueryData(["mock", slug], null);
  };

  if (q.isPending) return <p className="muted">Loading…</p>;
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
            <input className="cell mono" value={port} onChange={(e) => setPort(e.target.value)} placeholder="any free port" inputMode="numeric" aria-label="Port" />
          </label>
          <label className="field-row">
            <span className="field-label">Delay (ms)</span>
            <input className="cell mono" value={delay} onChange={(e) => setDelay(e.target.value)} placeholder="0" inputMode="numeric" aria-label="Delay" />
          </label>
          <button className="btn primary" onClick={start}>
            <Play size={14} /> Start mock server
          </button>
        </div>
        {error && <p className="inline-error">{error}</p>}
      </div>
    );
  }
  return (
    <div className="mock-panel">
      <div className="mock-running">
        <span className="verdict v-running">Running</span>
        <code className="mono">{m.url}</code>
        <button className="icon-btn" onClick={() => navigator.clipboard?.writeText(m.url)} aria-label="Copy mock URL" title="Copy URL">
          <Copy size={13} />
        </button>
        <button className="btn ghost small" onClick={stop}>
          <Square size={13} /> Stop
        </button>
      </div>
      <h2 className="sub-head">Routes</h2>
      {(m.routes?.length ?? 0) === 0 ? (
        <p className="muted">No routes yet: save examples on this collection's requests (Save as example on a response). New ones are picked up within a second.</p>
      ) : (
        <div className="scroll-x">
          <table className="data-table">
            <thead>
              <tr>
                <th>Method</th>
                <th>Path</th>
                <th>Request</th>
                <th>Examples</th>
              </tr>
            </thead>
            <tbody>
              {m.routes!.map((r) => (
                <tr key={r.method + r.path}>
                  <td className="mono">{r.method}</td>
                  <td className="mono">{r.path}</td>
                  <td>
                    <button className="link" onClick={() => select(`${slug}/${r.request}`)}>
                      {r.name}
                    </button>
                  </td>
                  <td className="muted small">{r.examples.map((e) => e.name).join(", ")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <h2 className="sub-head">Recent requests</h2>
      {(m.hits?.length ?? 0) === 0 ? (
        <p className="muted">
          None yet. Try <code className="mono">curl {m.url}{m.routes?.[0]?.path.replace(/\{[^}]+\}/g, "1") ?? "/"}</code>
        </p>
      ) : (
        <div className="scroll-x">
          <table className="data-table">
            <tbody>
              {[...m.hits!].reverse().slice(0, 50).map((h, i) => (
                <tr key={i}>
                  <td className="mono muted">{new Date(h.at).toLocaleTimeString()}</td>
                  <td className="num mono">{h.status}</td>
                  <td className="mono">
                    {h.method} {h.path}
                  </td>
                  <td className="muted small">{h.example ?? "no match"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
