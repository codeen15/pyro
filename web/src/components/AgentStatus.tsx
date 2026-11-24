import type { AgentStatus } from "../api";

// A load agent's status as neutral text beside a dot (colour never carries it alone).
// Shared by Admin → Agents and the load test page's agent picker.

const STATUS_CLASS: Record<AgentStatus["status"], string> = { available: "is-available", busy: "is-busy", offline: "is-offline", error: "is-error" };

/** "3 h ago" for Unix seconds. */
export function ago(sec: number): string {
  const s = Math.max(0, Math.floor(Date.now() / 1000 - sec));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

export function StatusDot({ status }: { status: AgentStatus["status"] }) {
  return <span className={`status-dot ${STATUS_CLASS[status]}`} aria-hidden />;
}

/** "offline (last seen 3 h ago)" for an offline agent seen before, else the bare status. */
export function statusText(a: Pick<AgentStatus, "status" | "lastSeen">): string {
  return a.status === "offline" && a.lastSeen > 0 ? `${a.status} (last seen ${ago(a.lastSeen)})` : a.status;
}
