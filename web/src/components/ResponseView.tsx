import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Segmented, Skeleton, Tabs, Tag, Tooltip } from "antd";
import { BookmarkPlus, Check, Copy, SendHorizontal, Terminal, X } from "../icons";
import { decodeBody, type PyroResponse, type SendResult, type TestResult, type Timings } from "../api";
import { HEAT_STEPS, formatBytes, formatMs, heatOf, type HeatStep } from "../heat";

type Tab = "body" | "tests" | "headers" | "cookies" | "timing";
const HOT: HeatStep[] = ["orange", "yellow", "white"];

export function ResponseView({ result, sending, onSaveExample }: { result?: SendResult; sending: boolean; onSaveExample?: () => void }) {
  const [tab, setTab] = useState<Tab>("body");
  // A hot response stays lit until the user has looked at where the time went.
  const [acknowledged, setAcknowledged] = useState(false);
  useEffect(() => setAcknowledged(false), [result]);
  useEffect(() => {
    if (tab === "timing") setAcknowledged(true);
  }, [tab, result]);

  if (sending && !result) return <ResponseSkeleton />;
  if (!result) {
    return (
      <section className="card response empty-response" aria-label="Response">
        <header className="response-head">
          <span className="response-title">Response</span>
        </header>
        <EmptyState icon={<SendHorizontal size={22} />}>
          Send the request to see the response. <kbd>Ctrl</kbd> + <kbd>Enter</kbd>
        </EmptyState>
      </section>
    );
  }

  const res = result.response;
  return (
    <section className={`card response ${sending ? "is-stale" : ""}`} aria-label="Response" aria-busy={sending}>
      <header className="response-head">
        <span className="response-title">Response</span>
        {res && <StatusLine res={res} tests={result.tests} lit={!acknowledged} onTiming={() => setTab("timing")} onTests={() => setTab("tests")} onSaveExample={onSaveExample} />}
        {res && (
          <Tabs
            className="response-tabs"
            size="small"
            activeKey={tab}
            onChange={(k) => setTab(k as Tab)}
            aria-label="Response parts"
            items={(["body", "tests", "headers", "cookies", "timing"] as Tab[]).map((t) => ({
              key: t,
              label: (
                <>
                  {t[0].toUpperCase() + t.slice(1)}
                  {t === "tests" && (result.tests?.length ?? 0) > 0 && <TestCount tests={result.tests!} />}
                  {t === "headers" && <span className="count">{res.headers?.length ?? 0}</span>}
                  {t === "cookies" && (res.cookies?.length ?? 0) > 0 && <span className="count">{res.cookies!.length}</span>}
                </>
              ),
            }))}
          />
        )}
      </header>
      {result.unresolved && result.unresolved.length > 0 && (
        <Alert
          type="warning"
          showIcon
          role="status"
          title={
            <>
              Not defined in the selected environment or collection:{" "}
              {result.unresolved.map((v, i) => (
                <span key={v}>
                  {i > 0 && ", "}
                  <code>{`{{${v}}}`}</code>
                </span>
              ))}
              . Sent as written.
            </>
          }
        />
      )}
      {result.error && <Alert type="error" showIcon title={result.error} />}
      {result.scriptErrors?.map((e, i) => <Alert key={i} type="error" showIcon title={<span className="mono">{e}</span>} />)}
      {result.skipped && <Alert type="info" showIcon role="status" title="Not sent: a pre-request script called skipRequest()." />}
      {res && (
        <div className="response-panel" role="tabpanel">
          {tab === "body" && <BodyView res={res} />}
          {tab === "tests" && <TestsView result={result} />}
          {tab === "headers" && <Table rows={(res.headers ?? []).map((h) => [h.key, h.value])} head={["Header", "Value"]} empty="No headers." />}
          {tab === "cookies" && (
            <Table
              head={["Name", "Value", "Domain", "Path", "Flags"]}
              rows={(res.cookies ?? []).map((c) => [c.name, c.value, c.domain ?? "", c.path ?? "", [c.httpOnly && "HttpOnly", c.secure && "Secure"].filter(Boolean).join(" ")])}
              empty="The server set no cookies."
            />
          )}
          {tab === "timing" && <TimingView t={res.timings} remote={res.remoteAddr} />}
        </div>
      )}
    </section>
  );
}

export function EmptyState({ icon, children, action }: { icon: React.ReactNode; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="empty-state">
      <span className="empty-icon" aria-hidden>
        {icon}
      </span>
      <div>{children}</div>
      {action}
    </div>
  );
}

function StatusLine({
  res,
  tests,
  lit,
  onTiming,
  onTests,
  onSaveExample,
}: {
  res: PyroResponse;
  tests?: TestResult[];
  lit: boolean;
  onTiming: () => void;
  onTests: () => void;
  onSaveExample?: () => void;
}) {
  const color = res.status >= 500 || res.status === 0 ? "error" : res.status >= 400 ? "warning" : res.status >= 300 ? "processing" : "success";
  const heat = heatOf(res.timings.total);
  return (
    <div className="status-line">
      <Tag color={color} className="status-tag" bordered>
        {res.status ? `${res.status} ` : ""}
        {res.statusText}
      </Tag>
      <button className={`time-chip ${lit && HOT.includes(heat) ? "is-lit" : ""}`} onClick={onTiming} title="Show where the time went">
        <span className={`swatch heat-${heat}`} aria-hidden />
        {formatMs(res.timings.total)}
      </button>
      <span className="meta">{formatBytes(res.size)}</span>
      <span className="meta">{res.proto}</span>
      {tests && tests.length > 0 && (
        <button className="tests-chip" onClick={onTests} title="Show test results">
          <TestCount tests={tests} long />
        </button>
      )}
      {onSaveExample && (
        <Button size="small" className="save-example" icon={<BookmarkPlus size={13} />} onClick={onSaveExample} title="Keep this response in the request's Examples tab">
          Save as example
        </Button>
      )}
    </div>
  );
}

export function TestCount({ tests, long }: { tests: TestResult[]; long?: boolean }) {
  const failed = tests.filter((t) => !t.passed).length;
  const cls = failed > 0 ? "count fail" : "count pass";
  if (!long) return <span className={cls}>{failed > 0 ? `${failed} failed` : tests.length}</span>;
  return (
    <span className={failed > 0 ? "tests-fail" : "tests-pass"}>
      {failed > 0 ? <X size={13} /> : <Check size={13} />}
      {failed > 0 ? `${failed} of ${tests.length} tests failed` : `${tests.length} test${tests.length > 1 ? "s" : ""} passed`}
    </span>
  );
}

export function TestsView({ result }: { result: SendResult }) {
  const tests = result.tests ?? [];
  const extracted = Object.entries(result.extracted ?? {});
  const logs = result.logs ?? [];
  if (tests.length === 0 && extracted.length === 0 && logs.length === 0 && !result.problems?.length) {
    return <p className="muted pad">No tests for this request. Add assertions or a post-response script in the Tests and Scripts tabs.</p>;
  }
  return (
    <div className="tests-view">
      {tests.length > 0 && (
        <ul className="test-list">
          {tests.map((t, i) => (
            <li key={i} className={t.passed ? "is-pass" : "is-fail"}>
              {t.passed ? <Check size={14} aria-label="passed" /> : <X size={14} aria-label="failed" />}
              <span className="test-name">{t.name}</span>
              <span className="test-src">{t.source}</span>
              {!t.passed && t.error && <span className="test-err mono">{t.error}</span>}
            </li>
          ))}
        </ul>
      )}
      {(extracted.length > 0 || (result.problems?.length ?? 0) > 0) && (
        <>
          <h3 className="sub-head">Extracted</h3>
          {extracted.length > 0 && <Table rows={extracted.map(([k, v]) => [`{{${k}}}`, v])} empty="" />}
          {result.problems?.map((p, i) => (
            <p key={i} className="inline-warn mono">
              {p}
            </p>
          ))}
        </>
      )}
      {logs.length > 0 && (
        <>
          <h3 className="sub-head">
            <Terminal size={13} /> Console
          </h3>
          <pre className="console mono">
            {logs.map((l, i) => (
              <span key={i} className={`log-${l.level}`}>
                {l.message}
                {"\n"}
              </span>
            ))}
          </pre>
        </>
      )}
    </div>
  );
}

function BodyView({ res }: { res: PyroResponse }) {
  const [mode, setMode] = useState<"pretty" | "raw">("pretty");
  const contentType = (res.headers ?? []).find((h) => h.key.toLowerCase() === "content-type")?.value ?? "";
  const bytes = useMemo(() => decodeBody(res.body), [res.body]);
  const { text, pretty, binary } = useMemo(() => {
    const text = new TextDecoder("utf-8", { fatal: false }).decode(bytes);
    const binary = !contentType.startsWith("text/") && /[\x00-\x08\x0e-\x1f]/.test(text.slice(0, 2000));
    let pretty = text;
    if (/json/i.test(contentType) || /^\s*[[{]/.test(text)) {
      try {
        pretty = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        /* not JSON after all */
      }
    }
    return { text, pretty, binary };
  }, [bytes, contentType]);

  if (bytes.length === 0) return <p className="muted pad">Empty body.</p>;
  if (contentType.startsWith("image/")) {
    return <img className="body-image" alt="Response image" src={`data:${contentType};base64,${res.body}`} />;
  }
  if (binary) return <p className="muted pad">Binary content ({contentType || "unknown type"}), {formatBytes(res.size)}. Preview not available.</p>;

  return (
    <div className="body-view">
      <div className="body-tools">
        <Segmented
          size="small"
          aria-label="Body format"
          value={mode}
          onChange={(v) => setMode(v as "pretty" | "raw")}
          options={[
            { value: "pretty", label: "Pretty" },
            { value: "raw", label: "Raw" },
          ]}
        />
        <span className="meta">{contentType || "no content-type"}</span>
        <Tooltip title="Copy body">
          <Button type="text" size="small" icon={<Copy size={14} />} onClick={() => navigator.clipboard?.writeText(text)} aria-label="Copy body" />
        </Tooltip>
      </div>
      {res.bodyTruncated && (
        <p className="inline-warn">Showing the first {formatBytes(bytes.length)} of {formatBytes(res.size)}.</p>
      )}
      <pre className="code-view mono">{mode === "pretty" ? pretty : text}</pre>
    </div>
  );
}

const PHASES: { key: keyof Timings; label: string; note: string }[] = [
  { key: "dns", label: "DNS lookup", note: "resolving the host name" },
  { key: "connect", label: "TCP connect", note: "opening the connection" },
  { key: "tls", label: "TLS handshake", note: "negotiating encryption" },
  { key: "send", label: "Request sent", note: "writing the request" },
  { key: "wait", label: "Waiting (TTFB)", note: "server processing until the first byte" },
  { key: "download", label: "Download", note: "receiving the body" },
];

function TimingView({ t, remote }: { t: Timings; remote?: string }) {
  const total = Math.max(t.total, 0.001);
  const phases = PHASES.map((p) => ({ ...p, ms: t[p.key] as number })).filter((p) => p.ms > 0 || p.key === "wait");
  const totalHeat = heatOf(t.total);
  return (
    <div className="timing">
      {/* The heated bar: each phase is drawn to scale and glows by its own duration. */}
      <div className="heat-bar" role="img" aria-label={`Total ${formatMs(t.total)}, ${phases.map((p) => `${p.label} ${formatMs(p.ms)}`).join(", ")}`}>
        {phases.map((p) => (
          <span key={p.key} className={`heat-seg heat-${heatOf(p.ms)}`} style={{ flexGrow: Math.max(p.ms / total, 0.004) }} title={`${p.label}: ${formatMs(p.ms)}`} />
        ))}
      </div>
      <table className="timing-table">
        <tbody>
          {phases.map((p) => (
            <tr key={p.key}>
              <td>
                <span className={`swatch heat-${heatOf(p.ms)}`} aria-hidden />
                {p.label}
                <span className="muted"> · {p.note}</span>
              </td>
              <td className="num">{formatMs(p.ms)}</td>
            </tr>
          ))}
          <tr className="total-row">
            <td>Total</td>
            <td className="num">
              <span className={`swatch heat-${totalHeat}`} aria-hidden />
              {formatMs(t.total)}
            </td>
          </tr>
        </tbody>
      </table>
      <p className="muted small">
        {t.reused ? "Reused an open connection, so no DNS, connect or TLS time." : "New connection."}
        {remote && <> Connected to <code>{remote}</code>.</>}
      </p>
      <HeatLegend current={totalHeat} />
    </div>
  );
}

export function HeatLegend({ current }: { current?: HeatStep }) {
  return (
    <ol className="heat-legend" aria-label="Heat scale">
      {HEAT_STEPS.map((s) => (
        <li key={s.step} className={current === s.step ? "is-current" : undefined}>
          <span className={`swatch heat-${s.step}`} aria-hidden />
          {s.label}
        </li>
      ))}
    </ol>
  );
}

function Table({ head, rows, empty }: { head?: string[]; rows: string[][]; empty: string }) {
  if (rows.length === 0) return <p className="muted pad">{empty}</p>;
  return (
    <table className="data-table">
      {head && (
        <thead>
          <tr>
            {head.map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
      )}
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            {r.map((c, j) => (
              <td key={j} className={j > 0 ? "mono" : "mono key"}>
                {c}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function ResponseSkeleton() {
  return (
    <section className="card response" aria-label="Response" aria-busy="true">
      <header className="response-head">
        <span className="response-title">Response</span>
      </header>
      <Skeleton active paragraph={{ rows: 6 }} />
    </section>
  );
}
