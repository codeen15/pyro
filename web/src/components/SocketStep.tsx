import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Input, InputNumber, Select, Space, Tabs, Tag } from "antd";
import { Activity, Plus, SendHorizontal, Trash2, Unplug } from "../icons";
import { api, type PyroRequest, type SavedMessage, type SendResult, type SocketWait } from "../api";
import { formatMs } from "../heat";
import { useApp } from "../store";
import { VarInput, VarScope } from "../vars";
import { KVEditor } from "./KVEditor";
import { AuthEditor, DeleteRequestButton, RequestTitle, SaveButton } from "./RequestEditor";
import { EmptyState, TestCount, TestsView } from "./ResponseView";
import { ArgsEditor, WsPayloadEditor } from "./MessageArgs";
import { KindLabel } from "./Plugins";
import { ProcessorList, TimerEditor, postList, preList, setPost, setPre } from "./Processors";
import { MessageRow, SocketIOSettings } from "./SessionConsole";
import { JsonTree, defaultDepth } from "./JsonTree";

// Send, ping and close requests: steps on a named connection that a connect
// request opened, so HTTP requests can run in between and feed what they
// return into the messages.

type Tab = "messages" | "wait" | "close" | "own" | "headers" | "auth" | "settings" | "pre" | "post";

const ACTION_LABEL: Record<string, string> = { send: "Send & receive", ping: "Ping", close: "Close" };

/** Opens connections (GET /connections), for the name suggestions and the open/not-open status. */
export function useConnections() {
  return useQuery({ queryKey: ["connections"], queryFn: api.connections, refetchInterval: 5000 });
}

export function SocketStep({ path, saved }: { path: string; saved: PyroRequest }) {
  const qc = useQueryClient();
  const draft = useApp((s) => s.drafts[path]);
  const setDraft = useApp((s) => s.setDraft);
  const clearDraft = useApp((s) => s.clearDraft);
  const environment = useApp((s) => s.environment);
  const sending = useApp((s) => !!s.sending[path]);
  const setSending = useApp((s) => s.setSending);
  const setResult = useApp((s) => s.setResult);
  const req = draft ?? saved;
  const dirty = draft !== undefined;
  const sio = req.kind === "socketio";
  const action = req.action ?? "send";
  const edit = (patch: Partial<PyroRequest>) => setDraft(path, { ...req, ...patch });
  const conns = useConnections();
  const name = req.connection ?? "";
  const own = name === "" && action !== "close";
  const open = (conns.data ?? []).filter((c) => c.kind === req.kind || !c.kind);
  const isOpen = name !== "" && open.some((c) => c.name === name);

  const save = useMutation({
    mutationFn: () => api.saveRequest(path, req),
    onSuccess: (r) => {
      qc.setQueryData(["request", path], r);
      clearDraft(path);
      qc.invalidateQueries({ queryKey: ["workspace"] });
    },
  });

  const send = async () => {
    if (sending) return;
    setSending(path, true);
    try {
      const res = await api.send({ path, request: req, environment: environment ?? undefined });
      setResult(path, res);
    } catch (e) {
      setResult(path, { error: (e as Error).message, startedAt: new Date().toISOString() });
    } finally {
      setSending(path, false);
      qc.invalidateQueries({ queryKey: ["connections"] });
    }
  };

  // Ctrl/Cmd+Enter runs the step, Ctrl/Cmd+S saves.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return;
      if (e.key === "Enter") {
        e.preventDefault();
        send();
      } else if (e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (dirty) save.mutate();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const pre = preList(req);
  const post = postList(req);
  const tabs: { id: Tab; label: string; count?: number }[] = [
    ...(action === "send"
      ? [
          { id: "messages" as Tab, label: "Messages", count: req.messages?.length },
          { id: "wait" as Tab, label: "Wait", count: req.wait?.for ? 1 : 0 },
        ]
      : []),
    ...(action === "close" ? [{ id: "close" as Tab, label: "Close" }] : []),
    ...(own
      ? [
          { id: "headers" as Tab, label: "Headers", count: (req.headers ?? []).filter((h) => !h.disabled).length },
          { id: "auth" as Tab, label: "Auth" },
          ...(sio ? [{ id: "settings" as Tab, label: "Socket.IO" }] : []),
        ]
      : []),
    { id: "pre", label: "Pre-processors", count: pre.length + (req.think ? 1 : 0) },
    { id: "post", label: "Post-processors", count: post.length },
  ];
  const [tab, setTab] = useState<Tab>(tabs[0].id);
  const activeTab = tabs.some((t) => t.id === tab) ? tab : tabs[0].id;

  return (
    <VarScope path={path}>
      <section className="card editor">
        <RequestTitle req={req} path={path} dirty={dirty} onName={(n) => edit({ name: n })}>
          <KindLabel value={req.kind} />
          <Tag className="action-tag">{ACTION_LABEL[action] ?? action}</Tag>
          <SaveButton dirty={dirty} saving={save.isPending} onSave={() => save.mutate()} />
          <DeleteRequestButton path={path} name={saved.name} />
        </RequestTitle>
        {save.error && <Alert type="error" showIcon title={`Couldn't save: ${save.error.message}`} />}

        <form
          className="urlbar"
          onSubmit={(e) => {
            e.preventDefault();
            send();
          }}
        >
          <Space.Compact block>
            <span className={`kind-tag ${sio ? "m-sio" : "m-ws"} ${isOpen ? "is-live" : ""}`}>{action.toUpperCase()}</span>
            {own ? (
              <VarInput
                className="url-input mono"
                value={req.url}
                onChange={(e) => edit({ url: e.target.value })}
                placeholder={sio ? "http://localhost:3000 (its own connection)" : "wss://echo.example.com/socket (its own connection)"}
                aria-label="URL"
                spellCheck={false}
              />
            ) : (
              <VarInput
                className="url-input mono"
                value={name}
                onChange={(e) => edit({ connection: e.target.value })}
                placeholder="default"
                aria-label="Connection name"
                spellCheck={false}
              />
            )}
            <Button
              className="send"
              type="primary"
              htmlType="submit"
              loading={sending}
              disabled={own ? !req.url.trim() : !name.trim()}
              icon={action === "close" ? <Unplug size={15} /> : action === "ping" ? <Activity size={15} /> : <SendHorizontal size={15} />}
              title="Run (Ctrl+Enter)"
            >
              {sending ? (action === "send" && req.wait?.for ? "Waiting" : "Running") : action === "close" ? "Close" : action === "ping" ? "Ping" : "Send"}
            </Button>
          </Space.Compact>
        </form>
        <ConnectionPicker
          action={action}
          name={name}
          own={own}
          isOpen={isOpen}
          open={open.map((c) => c.name)}
          onPick={(connection) => edit({ connection })}
          onOwn={() => edit({ connection: "" })}
        />

        <Tabs
          className="part-tabs"
          activeKey={activeTab}
          onChange={(k) => setTab(k as Tab)}
          aria-label="Step parts"
          items={tabs.map((t) => ({
            key: t.id,
            label: (
              <>
                {t.label}
                {!!t.count && <span className="count">{t.count}</span>}
              </>
            ),
          }))}
        />

        <div className="tab-panel" role="tabpanel">
          {activeTab === "messages" && <StepMessages sio={sio} messages={req.messages ?? []} onChange={(messages) => edit({ messages: messages.length ? messages : undefined })} />}
          {activeTab === "wait" && <WaitEditor sio={sio} wait={req.wait} onChange={(wait) => edit({ wait })} />}
          {activeTab === "close" && (
            <div className="auth-editor">
              <div className="field-row">
                <label className="field-label" htmlFor="close-code">
                  Close code
                </label>
                <InputNumber id="close-code" className="wide" min={1000} max={4999} value={req.closeCode} placeholder="1000" onChange={(v) => edit({ closeCode: v ?? undefined })} />
              </div>
              <div className="field-row">
                <label className="field-label" htmlFor="close-reason">
                  Reason
                </label>
                <Input id="close-reason" className="wide" maxLength={123} value={req.closeReason ?? ""} placeholder="Optional" onChange={(e) => edit({ closeReason: e.target.value || undefined })} />
              </div>
              <p className="muted small">
                {sio
                  ? "Socket.IO disconnects without a code; the code and reason apply to WebSocket connections."
                  : "1000 is a normal close; 3000–4999 are for applications. The reason is at most 123 bytes."}
              </p>
            </div>
          )}
          {activeTab === "headers" && <KVEditor label="Handshake headers" rows={req.headers ?? []} onChange={(headers) => edit({ headers })} keyPlaceholder="Header" />}
          {activeTab === "auth" && <AuthEditor auth={req.auth} onChange={(auth) => edit({ auth })} context={{ path, request: req }} />}
          {activeTab === "settings" && sio && <SocketIOSettings cfg={req.socketio ?? {}} onChange={(socketio) => edit({ socketio })} />}
          {activeTab === "pre" && (
            <div className="proc-tab">
              <section>
                <h3 className="sub-head">Think time</h3>
                <p className="muted small">Load tests and opted-in collection runs; manual sends don't wait.</p>
                <TimerEditor value={req.think} onChange={(think) => edit({ think })} inheritLabel="Inherit (folder/collection)" />
              </section>
              <section>
                <h3 className="sub-head">Pre-processors</h3>
                <ProcessorList phase="pre" items={pre} onChange={(items) => edit(setPre(req, items))} />
              </section>
            </div>
          )}
          {activeTab === "post" && (
            <div className="proc-tab">
              <section>
                <h3 className="sub-head">Post-processors</h3>
                <p className="muted small">They run on the message the wait matched, as if it were a response body.</p>
                <ProcessorList phase="post" items={post} onChange={(items) => edit(setPost(req, items))} />
              </section>
            </div>
          )}
        </div>
      </section>
    </VarScope>
  );
}

function ConnectionPicker({
  action,
  name,
  own,
  isOpen,
  open,
  onPick,
  onOwn,
}: {
  action: string;
  name: string;
  own: boolean;
  isOpen: boolean;
  open: string[];
  onPick: (n: string) => void;
  onOwn: () => void;
}) {
  if (own) {
    return (
      <p className="muted small conn-hint">
        No connection name: this request opens its own connection, runs, and closes it.{" "}
        <Button type="link" size="small" onClick={() => onPick("default")}>
          Use a named connection
        </Button>
      </p>
    );
  }
  const others = open.filter((n) => n !== name);
  return (
    <div className="conn-hint muted small" role="status">
      <span className={`conn-dot is-${isOpen ? "live" : "idle"}`} aria-hidden />
      {name.includes("{{") ? (
        <span>The name is filled in when the step runs.</span>
      ) : isOpen ? (
        <span>
          <code>{name}</code> is open.
        </span>
      ) : (
        <span>
          <code>{name || "default"}</code> isn't open. Run its connect request first.
        </span>
      )}
      {others.length > 0 && (
        <span className="conn-others">
          Open:
          {others.map((n) => (
            <Button key={n} size="small" type="link" onClick={() => onPick(n)}>
              {n}
            </Button>
          ))}
        </span>
      )}
      {action !== "close" && (
        <Button type="link" size="small" onClick={onOwn}>
          Use its own connection
        </Button>
      )}
    </div>
  );
}

function StepMessages({ sio, messages, onChange }: { sio: boolean; messages: SavedMessage[]; onChange: (m: SavedMessage[]) => void }) {
  const set = (i: number, patch: Partial<SavedMessage>) => onChange(messages.map((m, j) => (j === i ? { ...m, ...patch } : m)));
  const add = () => onChange([...messages, { content: "", event: sio ? "message" : undefined }]);
  return (
    <div className="step-messages">
      {messages.length === 0 && <p className="muted">No messages: the step only waits. Add one to send it first.</p>}
      {messages.map((m, i) => (
        <section key={i} className="step-message" aria-label={`Message ${i + 1}`}>
          <header className="step-message-head">
            <span className="sub-head">Message {i + 1}</span>
            {sio && (
              <>
                <VarInput className="mono event-input" aria-label={`Event of message ${i + 1}`} value={m.event ?? ""} placeholder="event" onChange={(e) => set(i, { event: e.target.value })} spellCheck={false} />
                <Checkbox checked={!!m.ack} onChange={(e) => set(i, { ack: e.target.checked || undefined })}>
                  Ask for an ack
                </Checkbox>
              </>
            )}
            <Button type="text" icon={<Trash2 size={13} />} onClick={() => onChange(messages.filter((_, j) => j !== i))} aria-label={`Remove message ${i + 1}`} />
          </header>
          {sio ? (
            <ArgsEditor value={m.content} onChange={(content) => set(i, { content })} />
          ) : (
            <WsPayloadEditor value={m.content} binary={!!m.binary} onChange={(content, binary) => set(i, { content, binary: binary || undefined })} />
          )}
        </section>
      ))}
      <Button icon={<Plus size={14} />} onClick={add}>
        Add message
      </Button>
      <p className="muted small">Messages are sent in order. Variables like {"{{token}}"} are filled in when sent, so a value an HTTP request extracted can go here.</p>
    </div>
  );
}

function WaitEditor({ sio, wait, onChange }: { sio: boolean; wait?: SocketWait; onChange: (w: SocketWait | undefined) => void }) {
  const w = wait ?? {};
  const kind = w.for || "";
  const set = (patch: Partial<SocketWait>) => {
    const next = { ...w, ...patch };
    (Object.keys(next) as (keyof SocketWait)[]).forEach((k) => !next[k] && delete next[k]);
    onChange(next.for ? next : undefined);
  };
  return (
    <div className="auth-editor wait-editor">
      <div className="field-row">
        <span className="field-label">Wait for</span>
        <Select
          aria-label="Wait for"
          className="wide"
          value={kind}
          onChange={(v: SocketWait["for"]) => set({ for: v })}
          options={[
            { value: "", label: "Nothing: continue once sent" },
            { value: "message", label: "A message that matches" },
            ...(sio ? [{ value: "ack", label: "The ack of the last message asking for one" }] : []),
            { value: "time", label: "A set time, collecting every message" },
          ]}
        />
      </div>
      {kind !== "" && (
        <div className="field-row">
          <label className="field-label" htmlFor="wait-timeout">
            {kind === "time" ? "For" : "Timeout"}
          </label>
          <Input id="wait-timeout" className="mono wide" value={w.timeout ?? ""} placeholder="10s" onChange={(e) => set({ timeout: e.target.value })} />
        </div>
      )}
      {kind === "message" && (
        <>
          {sio && (
            <div className="field-row">
              <label className="field-label" htmlFor="wait-event">
                Event
              </label>
              <VarInput id="wait-event" className="mono wide" value={w.event ?? ""} placeholder="Any event" onChange={(e) => set({ event: e.target.value })} spellCheck={false} />
            </div>
          )}
          <div className="field-row">
            <label className="field-label" htmlFor="wait-contains">
              Contains
            </label>
            <VarInput id="wait-contains" className="mono wide" value={w.contains ?? ""} placeholder="Text the message includes" onChange={(e) => set({ contains: e.target.value })} spellCheck={false} />
          </div>
          <div className="field-row">
            <label className="field-label" htmlFor="wait-regex">
              Matches regex
            </label>
            <VarInput id="wait-regex" className="mono wide" value={w.regex ?? ""} placeholder="^\{.*&quot;ok&quot;:true" onChange={(e) => set({ regex: e.target.value })} spellCheck={false} />
          </div>
          <div className="field-row">
            <label className="field-label" htmlFor="wait-jsonpath">
              JSONPath
            </label>
            <VarInput id="wait-jsonpath" className="mono wide" value={w.jsonpath ?? ""} placeholder="$.type" onChange={(e) => set({ jsonpath: e.target.value })} spellCheck={false} />
          </div>
          {w.jsonpath && (
            <div className="field-row">
              <label className="field-label" htmlFor="wait-equals">
                Equals
              </label>
              <VarInput id="wait-equals" className="mono wide" value={w.equals ?? ""} placeholder="Any value" onChange={(e) => set({ equals: e.target.value })} spellCheck={false} />
            </div>
          )}
        </>
      )}
      <p className="muted small">
        {kind === "message"
          ? "The first message received that meets every condition ends the wait; leave them all empty to take the next message. Messages that arrived since the last step count too, so a reply that came during an HTTP request isn't missed. Without a match before the timeout, the step fails."
          : kind === "ack"
            ? "Ends when the server acknowledges the last message sent with “Ask for an ack”."
            : kind === "time"
              ? "Collects every message for this long; the step's body is a JSON array of them."
              : "The step sends its messages and the next request starts at once."}
      </p>
    </div>
  );
}

/** The result of a send, ping or close step. */
export function SocketStepResult({ result, sending }: { result?: SendResult; sending: boolean }) {
  const [tab, setTab] = useState<"matched" | "messages" | "tests">("matched");
  if (!result) {
    return (
      <section className={`card response empty-response ${sending ? "is-stale" : ""}`} aria-label="Result" aria-busy={sending}>
        <header className="response-head">
          <span className="response-title">Result</span>
        </header>
        <EmptyState icon={<SendHorizontal size={22} />}>
          {sending ? "Waiting…" : "Run the step to see what it sent and received."} <kbd>Ctrl</kbd> + <kbd>Enter</kbd>
        </EmptyState>
      </section>
    );
  }
  const s = result.socket;
  const tests = result.tests ?? [];
  const messages = [...(s?.sent ?? []), ...(s?.received ?? [])].sort((a, b) => a.at.localeCompare(b.at) || a.seq - b.seq);
  return (
    <section className={`card response ${sending ? "is-stale" : ""}`} aria-label="Result" aria-busy={sending}>
      <header className="response-head">
        <span className="response-title">Result</span>
        {s && !result.error && (
          <span className="status-line" role="status">
            <Tag className="status-tag" color="success">
              {s.matched ? (s.matched.event ? `matched ${s.matched.event}` : "matched") : result.response?.statusText || s.action}
            </Tag>
            {s.connection ? (
              <span className="muted">
                on <code>{s.connection}</code>
              </span>
            ) : (
              s.own && <span className="muted">own connection</span>
            )}
            {s.rttMs !== undefined && s.rttMs > 0 && <span className="time-chip">{formatMs(s.rttMs)} round trip</span>}
            {s.waitMs !== undefined && s.waitMs > 0 && <span className="muted">waited {formatMs(s.waitMs)}</span>}
            {tests.length > 0 && <TestCount tests={tests} long />}
          </span>
        )}
        {s && (
          <Tabs
            className="response-tabs"
            size="small"
            activeKey={tab}
            onChange={(k) => setTab(k as typeof tab)}
            aria-label="Result parts"
            items={[
              { key: "matched", label: s.action === "send" && s.matched ? "Matched" : "Body" },
              { key: "messages", label: <>Messages {messages.length > 0 && <span className="count">{messages.length}</span>}</> },
              { key: "tests", label: <>Tests {tests.length > 0 && <TestCount tests={tests} />}</> },
            ]}
          />
        )}
      </header>
      {result.unresolved && result.unresolved.length > 0 && (
        <Alert type="warning" showIcon role="status" title={`Not defined: ${result.unresolved.map((v) => `{{${v}}}`).join(", ")}. Sent as written.`} />
      )}
      {result.error && <Alert type="error" showIcon role="alert" title={result.error} />}
      {result.scriptErrors?.map((e, i) => <Alert key={i} type="error" showIcon title={<span className="mono">{e}</span>} />)}
      {s && (
        <div className="response-panel" role="tabpanel">
          {tab === "matched" && <StepBody result={result} />}
          {tab === "messages" &&
            (messages.length === 0 ? (
              <p className="muted pad">Nothing sent or received.</p>
            ) : (
              <ol className="msg-list">
                {messages.map((m) => (
                  <MessageRow key={`${m.direction}-${m.seq}`} m={m} />
                ))}
              </ol>
            ))}
          {tab === "tests" && <TestsView result={result} />}
        </div>
      )}
    </section>
  );
}

function StepBody({ result }: { result: SendResult }) {
  const s = result.socket!;
  const text = useMemo(() => {
    const b = result.response?.body;
    if (!b) return "";
    try {
      return new TextDecoder().decode(Uint8Array.from(atob(b), (c) => c.charCodeAt(0)));
    } catch {
      return b;
    }
  }, [result.response?.body]);
  const json = useMemo(() => {
    try {
      return { ok: true, v: JSON.parse(text) as unknown };
    } catch {
      return { ok: false, v: undefined };
    }
  }, [text]);
  if (!text) {
    const what =
      s.action === "close"
        ? "The connection is closed."
        : s.action === "ping"
          ? "Pong received."
          : s.action === "connect"
            ? "Connected."
            : "Sent. The step didn't wait for a reply.";
    return <p className="muted pad">{what}</p>;
  }
  return (
    <div className="step-body">
      {s.matched?.event && (
        <p className="small">
          Event <span className="event-tag">{s.matched.event}</span>
        </p>
      )}
      {json.ok && json.v !== null && typeof json.v === "object" ? <JsonTree value={json.v} depth={defaultDepth(json.v)} /> : <pre className="code-view mono">{text}</pre>}
    </div>
  );
}
