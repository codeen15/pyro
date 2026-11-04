import { apiBase, sessionToken } from "./api";

// One shared WebSocket to <api>/events. Components subscribe to topics
// ("session:<id>", "run:<id>"); the socket reconnects and resubscribes on drops.

export interface PyroEvent<T = unknown> {
  topic: string;
  type: string;
  data: T;
}

type Listener = (e: PyroEvent) => void;

const listeners = new Map<string, Set<Listener>>();
let socket: WebSocket | null = null;
let retry = 0;

function url() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const t = sessionToken();
  return `${proto}//${location.host}${apiBase()}/events${t ? `?token=${encodeURIComponent(t)}` : ""}`;
}

function send(op: "subscribe" | "unsubscribe", topic: string) {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ op, topic }));
}

function connect() {
  socket = new WebSocket(url());
  socket.onopen = () => {
    retry = 0;
    for (const topic of listeners.keys()) send("subscribe", topic);
  };
  socket.onmessage = (msg) => {
    let ev: PyroEvent;
    try {
      ev = JSON.parse(msg.data);
    } catch {
      return;
    }
    listeners.get(ev.topic)?.forEach((fn) => fn(ev));
  };
  socket.onclose = () => {
    socket = null;
    if (listeners.size === 0) return;
    retry = Math.min(retry + 1, 6);
    setTimeout(() => {
      if (!socket && listeners.size > 0) connect();
    }, 250 * 2 ** retry);
  };
}

/** Subscribes to a topic; returns an unsubscribe function. */
export function subscribe<T>(topic: string, fn: (e: PyroEvent<T>) => void): () => void {
  let set = listeners.get(topic);
  if (!set) {
    set = new Set();
    listeners.set(topic, set);
    if (!socket) connect();
    else send("subscribe", topic);
  }
  set.add(fn as Listener);
  return () => {
    set!.delete(fn as Listener);
    if (set!.size === 0) {
      listeners.delete(topic);
      send("unsubscribe", topic);
    }
  };
}

/**
 * Resolves once the socket is open and subscribed, so events published
 * right after a POST aren't missed.
 */
export function ready(): Promise<void> {
  if (socket?.readyState === WebSocket.OPEN) return Promise.resolve();
  if (!socket) connect();
  return new Promise((resolve) => {
    const s = socket!;
    const done = () => {
      s.removeEventListener("open", done);
      resolve();
    };
    s.addEventListener("open", done);
  });
}
