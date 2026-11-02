import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Input, Select, Space, Tabs, Tag, Tooltip } from "antd";
import { SearchOutlined } from "@ant-design/icons";
import { ArrowDownLeft, ArrowLeftRight, ArrowUpRight, Bookmark, Plug, PlugZap, SendHorizontal, Trash2, Unplug } from "../icons";
import { api, type PyroRequest, type SavedMessage, type SessionMessage, type SessionState, type SocketIOConfig } from "../api";
import { subscribe } from "../events";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";
import { KVEditor } from "./KVEditor";
import { AuthEditor, RequestTitle, SaveButton } from "./RequestEditor";
import { EmptyState } from "./ResponseView";
import { KindLabel } from "./Plugins";

type Tab = "message" | "saved" | "headers" | "auth" | "settings";

interface Live {
  state: SessionState;
  messages: SessionMessage[];
}

/** Console for WebSocket and Socket.IO requests: connect, compose, watch the timeline. */
export function SessionConsole({ path, saved }: { path: string; saved: PyroRequest }) {
  const qc = useQueryClient();
  const draft = useApp((s) => s.drafts[path]);
  const setDraft = useApp((s) => s.setDraft);
  const clearDraft = useApp((s) => s.clearDraft);
  const environment = useApp((s) => s.environment);
  const req = draft ?? saved;
  const dirty = draft !== undefined;
  const sio = req.kind === "socketio";
  const edit = (patch: Partial<PyroRequest>) => setDraft(path, { ...req, ...patch });

  const [tab, setTab] = useState<Tab>("message");
  const [live, setLive] = useState<Live | null>(null);
  const [connecting, setConnecting] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [unresolved, setUnresolved] = useState<string[]>([]);
  const [compose, setCompose] = useState<SavedMessage>(() => ({
    content: req.messages?.[0]?.content ?? "",
    event: req.messages?.[0]?.event ?? (sio ? "message" : undefined),
    ack: false,
  }));
  const [sendError, setSendError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => api.saveRequest(path, req),
    onSuccess: (r) => {
      qc.setQueryData(["request", path], r);
      clearDraft(path);
      qc.invalidateQueries({ queryKey: ["workspace"] });
    },
  });

  const connected = !!live && !live.state.closed;

  // Live updates: subscribe first, then fetch the current log and merge by seq,
  // so nothing published in between is lost.
  const sessionId = live?.state.id;
  useEffect(() => {
    if (!sessionId) return;
    const merge = (incoming: SessionMessage[]) =>
      setLive((cur) => {
        if (!cur || cur.state.id !== sessionId) return cur;
        const bySeq = new Map(cur.messages.map((m) => [m.seq, m]));
        incoming.forEach((m) => bySeq.set(m.seq, m));
        return { ...cur, messages: [...bySeq.values()].sort((a, b) => a.seq - b.seq) };
      });
    const off = subscribe<SessionMessage | { error?: string }>(`session:${sessionId}`, (e) => {
      if (e.type === "message") merge([e.data as SessionMessage]);
      if (e.type === "closed") {
        setLive((cur) => (cur && cur.state.id === sessionId ? { ...cur, state: { ...cur.state, closed: true, error: (e.data as { error?: string }).error } } : cur));
      }
    });
    api.sessionState(sessionId).then((st) => merge(st.messages ?? []), () => {});
    return off;
  }, [sessionId]);

  // Close the session when leaving the request.
  useEffect(() => {
    return () => {
      if (sessionId) api.closeSession(sessionId).catch(() => {});
    };
  }, [sessionId]);

  const connect = async () => {
    setConnecting(true);
    setConnectError(null);
    try {
      const res = await api.openSession({ path, request: req, environment: environment ?? undefined });
      setUnresolved(res.unresolved ?? []);
      if (res.error || !res.session) {
        setConnectError(res.error ?? "Couldn't connect.");
      } else {
        setLive({ state: res.session, messages: res.session.messages ?? [] });
        setTab("message");
      }
    } catch (e) {
      setConnectError((e as Error).message);
    } finally {
      setConnecting(false);
    }
  };

  const disconnect = async () => {
    if (!live) return;
    await api.closeSession(live.state.id).catch(() => {});
    setLive((cur) => (cur ? { ...cur, state: { ...cur.state, closed: true } } : cur));
  };

  const send = async () => {
    if (!live || !connected) return;
    setSendError(null);
    try {
      const res = await api.sessionSend(live.state.id, {
        data: compose.content,
        event: sio ? compose.event : undefined,
        ack: sio ? compose.ack : undefined,
        binary: compose.binary,
      });
      setUnresolved(res.unresolved ?? []);
      setLive((cur) => (cur && !cur.messages.some((m) => m.seq === res.message.seq) ? { ...cur, messages: [...cur.messages, res.message] } : cur));
    } catch (e) {
      setSendError((e as Error).message);
    }
  };

  const saveTemplate = () => {
    const name = compose.event ? `${compose.event}` : compose.content.slice(0, 24) || "Message";
    edit({ messages: [...(req.messages ?? []), { ...compose, name }] });
  };

  // Ctrl/Cmd+Enter sends (or connects), Ctrl/Cmd+S saves.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return;
      if (e.key === "Enter") {
        e.preventDefault();
        if (connected) send();
        else if (!connecting) connect();
      } else if (e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (dirty) save.mutate();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const tabs: { id: Tab; label: string; count?: number }[] = [
    { id: "message", label: "Message" },
    { id: "saved", label: "Saved", count: req.messages?.length },
    { id: "headers", label: "Headers", count: (req.headers ?? []).filter((h) => !h.disabled).length },
    { id: "auth", label: "Auth" },
    ...(sio ? [{ id: "settings" as Tab, label: "Socket.IO" }] : []),
  ];

  return (
    <div className="work session">
      <section className="card editor">
        <RequestTitle req={req} path={path} dirty={dirty} onName={(name) => edit({ name })}>
          <KindLabel value={req.kind} />
          <SaveButton dirty={dirty} saving={save.isPending} onSave={() => save.mutate()} />
        </RequestTitle>

        <form
          className="urlbar"
          onSubmit={(e) => {
            e.preventDefault();
            if (connected) disconnect();
            else connect();
          }}
        >
          <Space.Compact block size="large">
            <span className={`kind-tag ${sio ? "m-sio" : "m-ws"} ${connected ? "is-live" : ""}`}>{sio ? "SIO" : "WS"}</span>
            <Input
              className="url-input mono"
              value={req.url}
              onChange={(e) => edit({ url: e.target.value })}
              placeholder={sio ? "http://localhost:3000" : "wss://echo.example.com/socket"}
              aria-label="URL"
              spellCheck={false}
              disabled={connected}
            />
            {connected ? (
              <Button className="send" htmlType="submit" danger icon={<Unplug size={15} />}>
                Disconnect
              </Button>
            ) : (
              <Button className="send" type="primary" htmlType="submit" loading={connecting} disabled={!req.url.trim()} icon={<PlugZap size={15} />} title="Connect (Ctrl+Enter)">
                {connecting ? "Connecting" : "Connect"}
              </Button>
            )}
          </Space.Compact>
        </form>
        {connectError && <Alert type="error" showIcon role="alert" title={connectError} />}

        <Tabs
          className="part-tabs"
          activeKey={tab}
          onChange={(k) => setTab(k as Tab)}
          aria-label="Session parts"
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
          {tab === "message" && (
            <div className="composer">
              {sio && (
                <div className="field-row">
                  <label className="field-label" htmlFor="sio-event">
                    Event
                  </label>
                  <Input id="sio-event" className="mono event-input" value={compose.event ?? ""} onChange={(e) => setCompose({ ...compose, event: e.target.value })} spellCheck={false} />
                  <Checkbox checked={!!compose.ack} onChange={(e) => setCompose({ ...compose, ack: e.target.checked })}>
                    Ask for an ack
                  </Checkbox>
                </div>
              )}
              <Input.TextArea
                className="code-input mono"
                value={compose.content}
                onChange={(e) => setCompose({ ...compose, content: e.target.value })}
                spellCheck={false}
                aria-label="Message"
                autoSize={{ minRows: 5 }}
                placeholder={sio ? '["hello", {"room": "{{room}}"}]   (a JSON array of arguments)' : '{"op": "subscribe", "channel": "{{channel}}"}'}
              />
              <div className="composer-actions">
                <Button type="primary" icon={<SendHorizontal size={14} />} onClick={send} disabled={!connected} title={connected ? "Send (Ctrl+Enter)" : "Connect first"}>
                  Send
                </Button>
                <Button icon={<Bookmark size={13} />} onClick={saveTemplate} disabled={!compose.content.trim() && !compose.event}>
                  Save message
                </Button>
                {!sio && (
                  <Checkbox checked={!!compose.binary} onChange={(e) => setCompose({ ...compose, binary: e.target.checked })}>
                    Binary (base64)
                  </Checkbox>
                )}
                {!connected && <span className="muted small">Connect to send. Variables like {"{{name}}"} are filled in when sent.</span>}
              </div>
              {sendError && <Alert type="error" showIcon title={sendError} />}
            </div>
          )}
          {tab === "saved" && (
            <SavedMessages
              messages={req.messages ?? []}
              onUse={(m) => {
                setCompose({ ...m });
                setTab("message");
              }}
              onChange={(messages) => edit({ messages: messages.length ? messages : undefined })}
            />
          )}
          {tab === "headers" && <KVEditor label="Handshake headers" rows={req.headers ?? []} onChange={(headers) => edit({ headers })} keyPlaceholder="Header" />}
          {tab === "auth" && <AuthEditor auth={req.auth} onChange={(auth) => edit({ auth })} context={{ path, request: req }} />}
          {tab === "settings" && sio && <SocketIOSettings cfg={req.socketio ?? {}} onChange={(socketio) => edit({ socketio })} />}
        </div>
      </section>

      <Timeline
        live={live}
        unresolved={unresolved}
        connecting={connecting}
        canConnect={!!req.url.trim()}
        onConnect={connect}
        onClear={() => setLive((cur) => (cur ? { ...cur, messages: [] } : cur))}
      />
    </div>
  );
}

function SavedMessages({ messages, onUse, onChange }: { messages: SavedMessage[]; onUse: (m: SavedMessage) => void; onChange: (m: SavedMessage[]) => void }) {
  if (messages.length === 0) {
    return <p className="muted">No saved messages. Compose one in the Message tab and choose “Save message”.</p>;
  }
  return (
    <ul className="saved-list">
      {messages.map((m, i) => (
        <li key={i}>
          {m.event && <span className="event-tag">{m.event}</span>}
          <span className="mono saved-preview">{m.content || "(no payload)"}</span>
          <Button type="link" size="small" onClick={() => onUse(m)} title="Load into the composer">
            Load
          </Button>
          <Button type="text" size="small" icon={<Trash2 size={13} />} onClick={() => onChange(messages.filter((_, j) => j !== i))} aria-label="Delete saved message" />
        </li>
      ))}
    </ul>
  );
}

function SocketIOSettings({ cfg, onChange }: { cfg: SocketIOConfig; onChange: (c: SocketIOConfig) => void }) {
  return (
    <div className="auth-editor">
      <label className="field-row">
        <span className="field-label">Namespace</span>
        <Input className="mono wide" value={cfg.namespace ?? ""} placeholder="/" onChange={(e) => onChange({ ...cfg, namespace: e.target.value || undefined })} />
      </label>
      <label className="field-row">
        <span className="field-label">Path</span>
        <Input className="mono wide" value={cfg.path ?? ""} placeholder="/socket.io/" onChange={(e) => onChange({ ...cfg, path: e.target.value || undefined })} />
      </label>
      <label className="field-row">
        <span className="field-label">Auth (JSON)</span>
        <Input className="mono wide" value={cfg.auth ?? ""} placeholder='{"token": "{{token}}"}' onChange={(e) => onChange({ ...cfg, auth: e.target.value || undefined })} />
      </label>
      <div className="field-row">
        <span className="field-label">Transport</span>
        <Select
          aria-label="Transport"
          className="wide"
          value={cfg.transport ?? "auto"}
          onChange={(v) => onChange({ ...cfg, transport: v === "auto" ? undefined : (v as SocketIOConfig["transport"]) })}
          options={[
            { value: "auto", label: "WebSocket, falling back to long-polling" },
            { value: "websocket", label: "WebSocket only" },
            { value: "polling", label: "Long-polling only" },
          ]}
        />
      </div>
      <div className="field-row">
        <span className="field-label">Version</span>
        <Tag>v4 (EIO 4)</Tag>
      </div>
      <p className="muted small">
        Auth is sent with the CONNECT packet (Socket.IO v3/v4). Binary values appear as <code>{'{"$binary": "<base64>"}'}</code> in messages; send one the same way inside your arguments.
      </p>
    </div>
  );
}

function Timeline({
  live,
  unresolved,
  connecting,
  canConnect,
  onConnect,
  onClear,
}: {
  live: Live | null;
  unresolved: string[];
  connecting: boolean;
  canConnect: boolean;
  onConnect: () => void;
  onClear: () => void;
}) {
  const [filter, setFilter] = useState("");
  const [follow, setFollow] = useState(true);
  const scroller = useRef<HTMLDivElement>(null);
  const messages = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const all = live?.messages ?? [];
    return q ? all.filter((m) => (m.data ?? "").toLowerCase().includes(q) || (m.event ?? "").toLowerCase().includes(q)) : all;
  }, [live, filter]);

  useEffect(() => {
    if (follow && scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [messages, follow]);

  const counts = useMemo(() => {
    const all = live?.messages ?? [];
    return { sent: all.filter((m) => m.direction === "sent").length, received: all.filter((m) => m.direction === "received").length };
  }, [live]);

  const state = !live ? "idle" : live.state.closed ? (live.state.error ? "error" : "closed") : "live";
  return (
    <section className="card response timeline" aria-label="Messages">
      <header className="timeline-head">
        <span className={`conn-dot is-${connecting ? "connecting" : state}`} aria-hidden />
        <span className="conn-label">
          {connecting
            ? "Connecting"
            : state === "live"
              ? "Connected"
              : state === "error"
                ? live!.state.error
                : state === "closed"
                  ? "Disconnected"
                  : "Not connected"}
        </span>
        {live && (
          <span className="meta">
            {state === "live" && live.state.info.sid ? `sid ${live.state.info.sid} · ` : ""}
            {state === "live" && live.state.info.subprotocol ? `${live.state.info.subprotocol} · ` : ""}
            {state === "live" && live.state.info.transport ? `${live.state.info.transport} · ` : ""}
            {counts.sent} sent · {counts.received} received
          </span>
        )}
        <span className="timeline-tools">
          <Input
            className="filter"
            allowClear
            prefix={<SearchOutlined />}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter messages"
            aria-label="Filter messages"
          />
          <Checkbox checked={follow} onChange={(e) => setFollow(e.target.checked)}>
            Follow
          </Checkbox>
          <Tooltip title="Clear timeline">
            <Button type="text" icon={<Trash2 size={14} />} onClick={onClear} aria-label="Clear timeline" />
          </Tooltip>
        </span>
      </header>
      {unresolved.length > 0 && <Alert type="warning" showIcon banner title={`Not defined: ${unresolved.map((v) => `{{${v}}}`).join(", ")}. Sent as written.`} />}
      <div className="timeline-scroll" ref={scroller}>
        {messages.length === 0 ? (
          live ? (
            <p className="muted pad">{filter ? "No messages match the filter." : "No messages yet."}</p>
          ) : (
            <EmptyState
              icon={<ArrowLeftRight size={22} />}
              action={
                <Button className="connect-outline" icon={<PlugZap size={14} />} onClick={onConnect} loading={connecting} disabled={!canConnect}>
                  Connect
                </Button>
              }
            >
              Connect to start the session. Messages in both directions appear here.
            </EmptyState>
          )
        ) : (
          <ol className="msg-list">
            {messages.map((m) => (
              <MessageRow key={m.seq} m={m} />
            ))}
          </ol>
        )}
      </div>
    </section>
  );
}

function MessageRow({ m }: { m: SessionMessage }) {
  const [open, setOpen] = useState(false);
  const pretty = useMemo(() => {
    if (!open || !m.data) return m.data;
    try {
      return JSON.stringify(JSON.parse(m.data), null, 2);
    } catch {
      return m.data;
    }
  }, [open, m.data]);
  const time = new Date(m.at).toLocaleTimeString(undefined, { hour12: false }) + "." + String(new Date(m.at).getMilliseconds()).padStart(3, "0");

  if (m.direction === "system") {
    return (
      <li className={`msg system ${m.kind === "error" ? "is-error" : ""}`}>
        <span className="msg-icon">
          <Plug size={13} />
        </span>
        <time>{time}</time>
        <span className="msg-system mono">{m.data}</span>
      </li>
    );
  }
  return (
    <li className={`msg ${m.direction}`}>
      <button className="msg-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="msg-icon">{m.direction === "sent" ? <ArrowUpRight size={13} aria-label="sent" /> : <ArrowDownLeft size={13} aria-label="received" />}</span>
        <time>{time}</time>
        <span className="msg-event">
          {m.kind === "ack" ? <span className="event-tag">ack #{m.ackId}</span> : m.event ? <span className="event-tag">{m.event}</span> : m.kind === "binary" ? <span className="meta">binary</span> : null}
        </span>
        <span className="msg-data mono">
          {m.ackId !== undefined && m.kind !== "ack" && <span className="meta">ack #{m.ackId} </span>}
          {m.data}
        </span>
        <span className="msg-size">
          {m.rttMs !== undefined && m.rttMs > 0 && (
            <span className="rtt" title="Ack round trip">
              <span className={`swatch heat-${heatOf(m.rttMs)}`} aria-hidden />
              {formatMs(m.rttMs)}
            </span>
          )}
          {m.size} B
        </span>
      </button>
      {open && <pre className="code-view mono msg-body">{pretty}</pre>}
    </li>
  );
}
