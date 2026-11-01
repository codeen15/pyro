import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowDownLeft, ArrowUpRight, Bookmark, Circle, Plug, PlugZap, Save, SendHorizontal, Trash2, X } from "lucide-react";
import { api, type PyroRequest, type SavedMessage, type SessionMessage, type SessionState, type SocketIOConfig } from "../api";
import { subscribe } from "../events";
import { formatMs, heatOf } from "../heat";
import { useApp } from "../store";
import { KVEditor } from "./KVEditor";
import { AuthEditor, RequestTitle } from "./RequestEditor";

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
      <section className="editor">
        <RequestTitle req={req} path={path} dirty={dirty} onName={(name) => edit({ name })}>
          <button className="btn ghost" onClick={() => save.mutate()} disabled={!dirty || save.isPending} title="Save (Ctrl+S)">
            <Save size={14} />
            {save.isPending ? "Saving…" : dirty ? "Save" : "Saved"}
          </button>
        </RequestTitle>

        <form
          className="urlbar"
          onSubmit={(e) => {
            e.preventDefault();
            if (connected) disconnect();
            else connect();
          }}
        >
          <span className={`kind-tag ${connected ? "is-live" : ""}`}>
            {sio ? "SIO" : "WS"}
          </span>
          <input
            className="url-input mono"
            value={req.url}
            onChange={(e) => edit({ url: e.target.value })}
            placeholder={sio ? "http://localhost:3000" : "wss://echo.example.com/socket"}
            aria-label="URL"
            spellCheck={false}
            disabled={connected}
          />
          {connected ? (
            <button className="btn ghost connect" type="submit">
              <X size={15} /> Disconnect
            </button>
          ) : (
            <button className="btn primary send" type="submit" disabled={connecting || !req.url.trim()} title="Connect (Ctrl+Enter)">
              <PlugZap size={15} />
              {connecting ? "Connecting…" : "Connect"}
            </button>
          )}
        </form>
        {connectError && (
          <p className="inline-error" role="alert">
            {connectError}
          </p>
        )}

        <div className="tabs" role="tablist" aria-label="Session parts">
          {tabs.map((t) => (
            <button key={t.id} role="tab" aria-selected={tab === t.id} className="tab" onClick={() => setTab(t.id)}>
              {t.label}
              {!!t.count && <span className="count">{t.count}</span>}
            </button>
          ))}
        </div>

        <div className="tab-panel" role="tabpanel">
          {tab === "message" && (
            <div className="composer">
              {sio && (
                <div className="field-row">
                  <label className="field-label" htmlFor="sio-event">
                    Event
                  </label>
                  <input id="sio-event" className="cell mono event-input" value={compose.event ?? ""} onChange={(e) => setCompose({ ...compose, event: e.target.value })} spellCheck={false} />
                  <label className="check">
                    <input type="checkbox" checked={!!compose.ack} onChange={(e) => setCompose({ ...compose, ack: e.target.checked })} />
                    Ask for an ack
                  </label>
                </div>
              )}
              <textarea
                className="code-input mono"
                value={compose.content}
                onChange={(e) => setCompose({ ...compose, content: e.target.value })}
                spellCheck={false}
                aria-label="Message"
                placeholder={sio ? '["hello", {"room": "{{room}}"}]   (a JSON array of arguments)' : '{"op": "subscribe", "channel": "{{channel}}"}'}
              />
              <div className="composer-actions">
                <button className="btn primary" onClick={send} disabled={!connected} title={connected ? "Send (Ctrl+Enter)" : "Connect first"}>
                  <SendHorizontal size={14} /> Send
                </button>
                {!sio && (
                  <label className="check">
                    <input type="checkbox" checked={!!compose.binary} onChange={(e) => setCompose({ ...compose, binary: e.target.checked })} />
                    Binary (base64)
                  </label>
                )}
                <button className="btn ghost small" onClick={saveTemplate} disabled={!compose.content.trim() && !compose.event}>
                  <Bookmark size={13} /> Save to request
                </button>
                {!connected && <span className="muted small">Connect to send. Variables like {"{{name}}"} are filled in when sent.</span>}
              </div>
              {sendError && <p className="inline-error">{sendError}</p>}
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
          {tab === "auth" && <AuthEditor auth={req.auth} onChange={(auth) => edit({ auth })} />}
          {tab === "settings" && sio && <SocketIOSettings cfg={req.socketio ?? {}} onChange={(socketio) => edit({ socketio })} />}
        </div>
      </section>

      <div className="splitter static" aria-hidden />

      <Timeline live={live} unresolved={unresolved} onClear={() => setLive((cur) => (cur ? { ...cur, messages: [] } : cur))} />
    </div>
  );
}

function SavedMessages({ messages, onUse, onChange }: { messages: SavedMessage[]; onUse: (m: SavedMessage) => void; onChange: (m: SavedMessage[]) => void }) {
  if (messages.length === 0) {
    return <p className="muted">No saved messages. Compose one in the Message tab and choose “Save to request”.</p>;
  }
  return (
    <ul className="saved-list">
      {messages.map((m, i) => (
        <li key={i}>
          <button className="saved-item" onClick={() => onUse(m)} title="Load into the composer">
            {m.event && <span className="event">{m.event}</span>}
            <span className="mono saved-preview">{m.content || "(no payload)"}</span>
          </button>
          <button className="icon-btn" onClick={() => onChange(messages.filter((_, j) => j !== i))} aria-label="Delete saved message">
            <Trash2 size={13} />
          </button>
        </li>
      ))}
    </ul>
  );
}

function SocketIOSettings({ cfg, onChange }: { cfg: SocketIOConfig; onChange: (c: SocketIOConfig) => void }) {
  return (
    <div className="auth-editor">
      <label className="field-row">
        <span className="field-label">Path</span>
        <input className="cell mono wide" value={cfg.path ?? ""} placeholder="/socket.io/" onChange={(e) => onChange({ ...cfg, path: e.target.value || undefined })} />
      </label>
      <label className="field-row">
        <span className="field-label">Namespace</span>
        <input className="cell mono wide" value={cfg.namespace ?? ""} placeholder="/" onChange={(e) => onChange({ ...cfg, namespace: e.target.value || undefined })} />
      </label>
      <label className="field-row">
        <span className="field-label">Auth (JSON)</span>
        <input className="cell mono wide" value={cfg.auth ?? ""} placeholder='{"token": "{{token}}"}' onChange={(e) => onChange({ ...cfg, auth: e.target.value || undefined })} />
      </label>
      <p className="muted small">Sent with the CONNECT packet (Socket.IO v3/v4). WebSocket transport only; binary payloads aren't supported yet.</p>
    </div>
  );
}

function Timeline({ live, unresolved, onClear }: { live: Live | null; unresolved: string[]; onClear: () => void }) {
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

  return (
    <section className="response timeline" aria-label="Messages">
      <div className="status-line">
        {live ? (
          live.state.closed ? (
            <span className={`conn ${live.state.error ? "is-error" : ""}`}>
              <Circle size={9} /> {live.state.error ? live.state.error : "Disconnected"}
            </span>
          ) : (
            <span className="conn is-live">
              <Plug size={13} /> Connected{live.state.info.sid ? ` · sid ${live.state.info.sid}` : ""}
              {live.state.info.subprotocol ? ` · ${live.state.info.subprotocol}` : ""}
            </span>
          )
        ) : (
          <span className="conn">
            <Circle size={9} /> Not connected
          </span>
        )}
        {live && (
          <span className="meta">
            {counts.sent} sent · {counts.received} received
          </span>
        )}
        <input className="cell filter" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter messages" aria-label="Filter messages" />
        <label className="check small">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
          Follow
        </label>
        <button className="icon-btn" onClick={onClear} aria-label="Clear timeline" title="Clear timeline">
          <Trash2 size={13} />
        </button>
      </div>
      {unresolved.length > 0 && (
        <p className="inline-warn">
          Not defined: {unresolved.map((v) => `{{${v}}}`).join(", ")}. Sent as written.
        </p>
      )}
      <div className="timeline-scroll" ref={scroller}>
        {messages.length === 0 ? (
          <p className="muted pad">
            {live ? (filter ? "No messages match the filter." : "No messages yet.") : "Connect to start the session. Messages in both directions appear here."}
          </p>
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
        <time>{time}</time>
        <span>{m.data}</span>
      </li>
    );
  }
  return (
    <li className={`msg ${m.direction}`}>
      <button className="msg-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <time>{time}</time>
        {m.direction === "sent" ? <ArrowUpRight size={14} aria-label="sent" /> : <ArrowDownLeft size={14} aria-label="received" />}
        {m.kind === "ack" ? <span className="event">ack #{m.ackId}</span> : m.event && <span className="event">{m.event}</span>}
        {m.ackId !== undefined && m.kind !== "ack" && <span className="meta">ack #{m.ackId}</span>}
        {m.kind === "binary" && <span className="meta">binary</span>}
        <span className="msg-data mono">{m.data}</span>
        {m.rttMs !== undefined && m.rttMs > 0 && <span className={`heat-chip small heat-${heatOf(m.rttMs)}`} title="Ack round trip">{formatMs(m.rttMs)}</span>}
        <span className="meta">{m.size} B</span>
      </button>
      {open && <pre className="code-view mono msg-body">{pretty}</pre>}
    </li>
  );
}
