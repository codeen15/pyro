import { useMemo, useState } from "react";
import { Alert, App as AntApp, Button, Dropdown, Empty, Input, Result, Segmented, Select, Skeleton, Table, Tag, Tooltip } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Copy, Download, Gauge, Layers, Printer, Report, Square, Trash2 } from "../icons";
import { api, type ExportFormat, type ReportItem, type ReportStatus, type RunReport } from "../api";
import { formatMs } from "../heat";
import { useApp } from "../store";
import { useCanEdit, useTeam } from "../team";
import { RunResultsTable } from "./RunnerView";

// Test reports: every collection run and load test, for the SQA team to
// review, compare and file (PDF, Excel, CSV, JUnit).

const STATUS: Record<ReportStatus, { label: string; icon: typeof CircleCheck; cls: string }> = {
  passed: { label: "Passed", icon: CircleCheck, cls: "st-pass" },
  failed: { label: "Failed", icon: CircleX, cls: "st-fail" },
  aborted: { label: "Aborted", icon: CircleX, cls: "st-fail" },
  stopped: { label: "Stopped", icon: Square, cls: "st-stop" },
};

export function StatusLabel({ status }: { status: ReportStatus }) {
  const s = STATUS[status] ?? STATUS.stopped;
  return (
    <span className={`report-status ${s.cls}`}>
      <s.icon size={14} />
      {s.label}
    </span>
  );
}

function when(iso: string) {
  return new Date(iso).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function duration(secs: number) {
  return secs < 60 ? formatMs(secs * 1000) : `${Math.floor(secs / 60)} min ${Math.round(secs % 60)} s`;
}

/** Downloads (or, for HTML, opens and prints) a report export. */
async function exportReport(id: string, format: ExportFormat, print = false) {
  const { blob, name } = await api.exportReport(id, format);
  const url = URL.createObjectURL(blob);
  if (format === "html") {
    window.open(url + (print ? "#print" : ""), "_blank", "noopener");
  } else {
    const a = document.createElement("a");
    a.href = url;
    a.download = name ?? `report.${format === "junit" ? "xml" : format}`;
    a.click();
  }
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

/** The shareable address of a report. */
function reportLink(id: string) {
  const pid = useTeam.getState().project?.id;
  return `${location.origin}${pid ? `/p/${encodeURIComponent(pid)}` : ""}/reports/${id}`;
}

function ExportMenu({ id, kind }: { id: string; kind: "run" | "load" }) {
  const { message } = AntApp.useApp();
  const items = [
    { key: "xlsx", label: "Excel workbook (.xlsx)" },
    { key: "csv", label: "CSV (.csv)" },
    ...(kind === "run" ? [{ key: "junit", label: "JUnit XML (.xml)" }] : []),
    { key: "json", label: "JSON (.json)" },
    { key: "html", label: "HTML file (.html)" },
  ];
  return (
    <Dropdown
      trigger={["click"]}
      menu={{
        items,
        onClick: ({ key }) =>
          (key === "html" ? api.exportReport(id, "html").then(({ blob, name }) => {
            const a = document.createElement("a");
            a.href = URL.createObjectURL(blob);
            a.download = name ?? "report.html";
            a.click();
          }) : exportReport(id, key as ExportFormat)).catch((e: Error) => message.error(e.message)),
      }}
    >
      <Button icon={<Download size={14} />}>Download</Button>
    </Dropdown>
  );
}

export function ReportsPage() {
  const select = useApp((s) => s.select);
  const [kind, setKind] = useState<"all" | "run" | "load">("all");
  const [status, setStatus] = useState<"" | ReportStatus>("");
  const [search, setSearch] = useState("");
  const q = useQuery({ queryKey: ["reports"], queryFn: () => api.reports() });

  const rows = useMemo(() => {
    const s = search.trim().toLowerCase();
    return (q.data ?? []).filter(
      (r) =>
        (kind === "all" || r.kind === kind) &&
        (!status || r.status === status || (status === "failed" && r.status === "aborted")) &&
        (!s || r.title.toLowerCase().includes(s) || r.target.toLowerCase().includes(s) || (r.runBy ?? "").toLowerCase().includes(s) || (r.environment ?? "").toLowerCase().includes(s)),
    );
  }, [q.data, kind, status, search]);

  const counts = useMemo(() => {
    const all = q.data ?? [];
    return { passed: all.filter((r) => r.status === "passed").length, failed: all.filter((r) => r.status === "failed" || r.status === "aborted").length };
  }, [q.data]);

  return (
    <div className="page">
      <section className="card reports-page">
        <header className="page-head">
          <div>
            <h1>Reports</h1>
            <p className="muted small">
              Every collection run and load test in this project, newest first.
              {q.data && q.data.length > 0 && ` ${counts.passed} passed, ${counts.failed} failed.`}
            </p>
          </div>
        </header>
        <div className="report-filters">
          <Segmented
            aria-label="Kind"
            value={kind}
            onChange={(v) => setKind(v as typeof kind)}
            options={[
              { value: "all", label: "All" },
              { value: "run", label: "Collection runs" },
              { value: "load", label: "Load tests" },
            ]}
          />
          <Select
            aria-label="Status"
            value={status}
            onChange={setStatus}
            style={{ width: 140 }}
            options={[
              { value: "", label: "Any status" },
              { value: "passed", label: "Passed" },
              { value: "failed", label: "Failed" },
              { value: "stopped", label: "Stopped" },
            ]}
          />
          <Input allowClear placeholder="Search name, environment or person" aria-label="Search reports" value={search} onChange={(e) => setSearch(e.target.value)} className="report-search" />
        </div>
        {q.error ? (
          <Alert type="error" showIcon message={(q.error as Error).message} />
        ) : (
          <Table<ReportItem>
            rowKey="id"
            size="small"
            loading={q.isPending}
            dataSource={rows}
            scroll={{ x: "max-content" }}
            pagination={{ pageSize: 50, hideOnSinglePage: true }}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No reports yet. Run a collection or a load test and its report appears here." /> }}
            onRow={(r) => ({ onClick: () => select(r.kind === "run" ? `report:${r.id}` : `loadrun:${r.id}`), className: "report-row" })}
            columns={[
              { title: "Result", width: 110, render: (_, r) => <StatusLabel status={r.status} /> },
              {
                title: "Name",
                render: (_, r) => (
                  <span className="report-name">
                    <button className="link-button" onClick={(e) => { e.stopPropagation(); select(r.kind === "run" ? `report:${r.id}` : `loadrun:${r.id}`); }}>
                      {r.kind === "run" ? <Layers size={14} /> : <Gauge size={14} />}
                      {r.title}
                    </button>
                    <span className="muted mono small">{r.target}</span>
                  </span>
                ),
              },
              { title: "Kind", width: 120, render: (_, r) => <Tag>{r.kind === "run" ? "Collection run" : "Load test"}</Tag> },
              {
                title: "Outcome",
                render: (_, r) =>
                  r.kind === "run" ? (
                    <span className="small">
                      {r.requests} requests{r.failed ? <>, <strong className="fail-text">{r.failed} failed</strong></> : ""}
                      {r.tests ? <> · {r.tests} tests{r.testsFailed ? <>, <strong className="fail-text">{r.testsFailed} failed</strong></> : ""}</> : null}
                    </span>
                  ) : (
                    <span className="small">
                      {r.requests} requests · p95 {formatMs(r.p95 ?? 0)} · {((r.errorRate ?? 0) * 100).toFixed(2)}% errors · {r.maxVUs} VUs
                    </span>
                  ),
              },
              { title: "Environment", render: (_, r) => r.environment || <span className="muted">none</span> },
              { title: "Run by", render: (_, r) => r.runBy || <span className="muted">—</span> },
              { title: "Started", render: (_, r) => when(r.startedAt) },
              { title: "Duration", align: "right", render: (_, r) => <span className="mono">{duration(r.duration)}</span> },
            ]}
          />
        )}
      </section>
    </div>
  );
}

export function ReportPage({ id }: { id: string }) {
  const qc = useQueryClient();
  const select = useApp((s) => s.select);
  const canEdit = useCanEdit();
  const { message, modal } = AntApp.useApp();
  const q = useQuery({ queryKey: ["report", id], queryFn: () => api.report(id), retry: false });
  const cmp = useQuery({ queryKey: ["report-compare", id], queryFn: () => api.compareReport(id), retry: false, enabled: q.isSuccess });

  if (q.isPending) return <div className="page"><section className="card reports-page"><Skeleton active /></section></div>;
  if (q.error || !q.data) {
    return (
      <div className="page">
        <Result status="warning" title="This report isn't available" subTitle={(q.error as Error)?.message} extra={<Button onClick={() => select("reports")}>All reports</Button>} />
      </div>
    );
  }
  const r: RunReport = q.data;
  const t = r.totals;
  const passed = t.requests - t.skipped - t.requestsFailed;
  const ran = t.requests - t.skipped;
  const rate = ran > 0 ? (100 * passed) / ran : 100;
  const failures = r.summary.results.filter((it) => it && !it.note && it.result && (it.result.error || (it.result.scriptErrors?.length ?? 0) > 0 || (it.result.tests ?? []).some((x) => !x.passed)));
  const c = cmp.data;

  return (
    <div className="page">
      <section className="card reports-page report-detail">
        <header className="page-head">
          <div>
            <p className="muted small report-crumb">
              <button className="link-button" onClick={() => select("reports")}>
                <Report size={13} /> Reports
              </button>
            </p>
            <h1>
              {r.title} <StatusLabel status={r.status} />
            </h1>
            <p className="muted small">
              Collection run of <code>{r.target}</code> · {when(r.startedAt)}
            </p>
          </div>
          <div className="page-actions">
            <Button
              type="primary"
              icon={<Printer size={14} />}
              onClick={() => exportReport(id, "html", true).catch((e: Error) => message.error(e.message))}
            >
              Save as PDF
            </Button>
            <ExportMenu id={id} kind="run" />
            <Tooltip title="Copy a link to this report">
              <Button
                icon={<Copy size={14} />}
                aria-label="Copy link"
                onClick={() => navigator.clipboard.writeText(reportLink(id)).then(() => message.success("Link copied"), () => message.error("Couldn't copy"))}
              />
            </Tooltip>
            {canEdit && (
              <Tooltip title="Delete report">
                <Button
                  danger
                  icon={<Trash2 size={14} />}
                  aria-label="Delete report"
                  onClick={() =>
                    modal.confirm({
                      title: "Delete this report?",
                      content: "It's removed for everyone in the project.",
                      okText: "Delete",
                      okButtonProps: { danger: true },
                      onOk: async () => {
                        await api.deleteReport(id);
                        qc.invalidateQueries({ queryKey: ["reports"] });
                        select("reports");
                      },
                    })
                  }
                />
              </Tooltip>
            )}
          </div>
        </header>

        <dl className="report-meta">
          {r.project && (
            <>
              <dt>Project</dt>
              <dd>{r.project}</dd>
            </>
          )}
          <dt>Environment</dt>
          <dd>{r.environment || <span className="muted">none</span>}</dd>
          <dt>Run by</dt>
          <dd>{r.runBy || <span className="muted">—</span>}</dd>
          <dt>Duration</dt>
          <dd>{duration(r.duration)}</dd>
          <dt>Iterations</dt>
          <dd>
            {t.iterations}
            {r.dataFile && <span className="muted"> · data {r.dataFile}</span>}
          </dd>
          {r.version && (
            <>
              <dt>Pyro</dt>
              <dd>{r.version}</dd>
            </>
          )}
          {r.summary.stopped && (
            <>
              <dt>Stopped</dt>
              <dd>{r.summary.stopped}</dd>
            </>
          )}
        </dl>

        <div className="report-kpis" role="list" aria-label="Summary">
          <div role="listitem" className="report-kpi">
            <span>Requests passed</span>
            <b>
              {passed}
              <small>/{ran}</small>
            </b>
          </div>
          <div role="listitem" className="report-kpi">
            <span>Requests failed</span>
            <b className={t.requestsFailed ? "fail-text" : undefined}>{t.requestsFailed}</b>
          </div>
          <div role="listitem" className="report-kpi">
            <span>Tests failed</span>
            <b className={t.testsFailed ? "fail-text" : undefined}>
              {t.testsFailed}
              <small>/{t.tests}</small>
            </b>
          </div>
          <div role="listitem" className="report-kpi">
            <span>Pass rate</span>
            <b>{rate.toFixed(1)}%</b>
          </div>
          {t.skipped > 0 && (
            <div role="listitem" className="report-kpi">
              <span>Skipped</span>
              <b>{t.skipped}</b>
            </div>
          )}
        </div>

        <h2 className="report-h2">Compared with the previous run</h2>
        {cmp.isPending ? (
          <Skeleton active paragraph={{ rows: 1 }} title={false} />
        ) : !c ? (
          <p className="muted small">This is the first saved run of {r.target}.</p>
        ) : c.newlyFailing.length + c.newlyPassing.length + c.added.length + c.removed.length === 0 ? (
          <p className="small">
            No changes since the run on {when(c.againstStartedAt)}: the same tests passed and failed.{" "}
            <button className="link-button" onClick={() => select(`report:${c.against}`)}>
              Open that run
            </button>
          </p>
        ) : (
          <div className="report-changes">
            <p className="muted small">
              Against the run on {when(c.againstStartedAt)}.{" "}
              <button className="link-button" onClick={() => select(`report:${c.against}`)}>
                Open that run
              </button>
            </p>
            <ul>
              {c.newlyFailing.map((x) => (
                <li key={`f-${x.path}-${x.name}`}>
                  <span className="report-status st-fail">
                    <CircleX size={14} /> Now failing
                  </span>{" "}
                  {x.name}
                </li>
              ))}
              {c.newlyPassing.map((x) => (
                <li key={`p-${x.path}-${x.name}`}>
                  <span className="report-status st-pass">
                    <CircleCheck size={14} /> Now passing
                  </span>{" "}
                  {x.name}
                </li>
              ))}
              {c.added.map((p) => (
                <li key={`a-${p}`}>
                  <span className="muted">New request</span> <code>{p}</code>
                </li>
              ))}
              {c.removed.map((p) => (
                <li key={`r-${p}`}>
                  <span className="muted">No longer run</span> <code>{p}</code>
                </li>
              ))}
            </ul>
          </div>
        )}

        {failures.length > 0 && (
          <>
            <h2 className="report-h2">Failures ({failures.length})</h2>
            <ul className="report-failures">
              {failures.map((it, i) => (
                <li key={i}>
                  <strong>{it.name}</strong>
                  {t.iterations > 1 && <span className="muted small"> · iteration {it.iteration + 1}</span>}
                  <span className="muted mono small"> {it.path}</span>
                  <ul>
                    {it.result?.error && <li>{it.result.error}</li>}
                    {it.result?.scriptErrors?.map((e, j) => <li key={`s${j}`}>{e}</li>)}
                    {it.result?.tests
                      ?.filter((x) => !x.passed)
                      .map((x, j) => (
                        <li key={`t${j}`}>
                          {x.name}
                          {x.error && <span className="muted">: {x.error}</span>}
                        </li>
                      ))}
                  </ul>
                </li>
              ))}
            </ul>
          </>
        )}

        <h2 className="report-h2">All requests</h2>
        <p className="muted small">Click a row for the request sent and its tests. Secret values and credentials are masked.</p>
        <RunResultsTable results={r.summary.results} />
      </section>
    </div>
  );
}
