import { useCallback, useMemo, useState } from "react";
import { Checkbox, Radio } from "antd";
import type { AgentStatus } from "../api";
import { StatusDot, statusText } from "./AgentStatus";

// "Run on" for a load test: this server, or a checklist of load agents. Available
// agents are ticked by default; the choice is remembered per load test in the browser.

export type RunOn = "local" | "agents";

const key = (test: string) => `pyro.loadtest.agents.${test}`;

function readStored(test: string): string[] | null {
  try {
    const raw = localStorage.getItem(key(test));
    const v = raw ? JSON.parse(raw) : null;
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : null;
  } catch {
    return null;
  }
}

/** Only agents that are free can take part in a run. */
export const selectable = (a: AgentStatus) => a.status === "available";

/**
 * The agents ticked for a load test. Until the user ticks something, every
 * available agent is; a remembered selection drops agents that no longer exist
 * or can't be used right now.
 */
export function useAgentSelection(test: string, agents: AgentStatus[] | undefined) {
  // The stored list is kept in state so the checklist re-renders; it is re-read when the test changes.
  const [saved, setSaved] = useState<{ test: string; ids: string[] | null }>(() => ({ test, ids: readStored(test) }));
  const stored = saved.test === test ? saved.ids : readStored(test);

  const selected = useMemo(() => {
    const usable = (agents ?? []).filter(selectable).map((a) => a.id);
    if (stored === null) return usable;
    const known = new Set(usable);
    return stored.filter((id) => known.has(id));
  }, [agents, stored]);

  const set = useCallback(
    (ids: string[]) => {
      setSaved({ test, ids });
      try {
        localStorage.setItem(key(test), JSON.stringify(ids));
      } catch {
        // Private windows and blocked storage: the choice just isn't remembered.
      }
    },
    [test],
  );
  return { selected, set };
}

export function RunOnControl({
  value,
  onChange,
  agents,
}: {
  value: RunOn;
  onChange: (v: RunOn) => void;
  agents: AgentStatus[];
}) {
  const free = agents.filter(selectable).length;
  return (
    <Radio.Group className="run-on" aria-label="Run on" value={value} onChange={(e) => onChange(e.target.value)}>
      <Radio value="local">This server</Radio>
      <Radio value="agents">Agents ({free} available)</Radio>
    </Radio.Group>
  );
}

export function AgentChecklist({
  agents,
  selected,
  onChange,
}: {
  agents: AgentStatus[];
  selected: string[];
  onChange: (ids: string[]) => void;
}) {
  const on = new Set(selected);
  const toggle = (id: string, checked: boolean) => {
    const next = new Set(on);
    if (checked) next.add(id);
    else next.delete(id);
    // Keep the agents' own order.
    onChange(agents.filter((a) => next.has(a.id)).map((a) => a.id));
  };
  return (
    <div className="agent-pick" role="group" aria-label="Agents to run on">
      <p className="small agent-pick-count" aria-live="polite">
        {selected.length} of {agents.length} selected
      </p>
      <ul className="agent-pick-list small">
        {agents.map((a) => {
          const ok = selectable(a);
          return (
            <li key={a.id} className={ok ? undefined : "is-unavailable"}>
              <Checkbox checked={on.has(a.id)} disabled={!ok} onChange={(e) => toggle(a.id, e.target.checked)}>
                <span className="mono">{a.name || a.address}</span>
              </Checkbox>
              <span className="muted agent-pick-cpus">{ok && a.cpus > 0 ? `${a.cpus} CPUs` : ""}</span>
              <span className="agent-pick-status">
                <StatusDot status={a.status} />
                {statusText(a)}
              </span>
              {!ok && a.detail && <span className="muted agent-pick-detail">{a.detail}</span>}
            </li>
          );
        })}
      </ul>
      <p className="muted small">The plan's VUs and arrival rates are split evenly between the selected agents; results are merged here.</p>
    </div>
  );
}
