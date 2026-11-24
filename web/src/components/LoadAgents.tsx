import { useCallback, useId, useMemo, useState } from "react";
import { Checkbox, Radio } from "antd";
import type { AgentStatus } from "../api";
import { useTeam } from "../team";
import { StatusDot, statusText } from "./AgentStatus";

// "Run on" for a load test: this server, or a checklist of load agents. Every
// available agent is ticked by default; what the user unticks is remembered per
// project and load test in the browser, so agents that are added later (or come
// back online) are ticked again.

export type RunOn = "local" | "agents";

/** Agent ids are global on a team server and load test names are per project, so the key has both. */
export const agentsKey = (project: string | undefined, test: string) => `pyro.loadtest.agents.${project ?? "local"}.${test}`;

function readUnticked(key: string): string[] {
  try {
    const raw = localStorage.getItem(key);
    const v = raw ? JSON.parse(raw) : null;
    return Array.isArray(v?.unticked) ? v.unticked.filter((x: unknown): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

/** Only agents that are free can take part in a run. */
export const selectable = (a: AgentStatus) => a.status === "available";

/** The agents ticked for a load test: the available ones, minus those the user unticked. */
export function useAgentSelection(test: string, agents: AgentStatus[] | undefined) {
  const project = useTeam((s) => s.project?.id);
  const key = agentsKey(project, test);
  // Kept in state so the checklist re-renders; re-read when the load test or project changes.
  const [saved, setSaved] = useState<{ key: string; ids: string[] }>(() => ({ key, ids: readUnticked(key) }));
  const unticked = saved.key === key ? saved.ids : readUnticked(key);

  const selected = useMemo(() => {
    const off = new Set(unticked);
    return (agents ?? []).filter((a) => selectable(a) && !off.has(a.id)).map((a) => a.id);
  }, [agents, unticked]);

  const set = useCallback(
    (ticked: string[]) => {
      // Remember the unticked ones that still exist (so removed agents are dropped).
      const on = new Set(ticked);
      const ids = (agents ?? []).filter((a) => !on.has(a.id) && (selectable(a) || unticked.includes(a.id))).map((a) => a.id);
      setSaved({ key, ids });
      try {
        localStorage.setItem(key, JSON.stringify({ unticked: ids }));
      } catch {
        // Private windows and blocked storage: the choice just isn't remembered.
      }
    },
    [agents, key, unticked],
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
  const uid = useId();
  const on = new Set(selected);
  const toggle = (id: string, checked: boolean) => {
    const next = new Set(on);
    if (checked) next.add(id);
    else next.delete(id);
    onChange(agents.filter((a) => next.has(a.id)).map((a) => a.id));
  };
  return (
    <div className="agent-pick" role="group" aria-label="Agents to run on">
      <p className="small agent-pick-count" aria-live="polite">
        {selected.length} of {agents.length} selected
      </p>
      <ul className="agent-pick-list small">
        {agents.map((a, i) => {
          const ok = selectable(a);
          const said = `${uid}-${i}`;
          return (
            <li key={a.id} className={ok ? undefined : "is-unavailable"}>
              <Checkbox checked={on.has(a.id)} disabled={!ok} aria-describedby={said} onChange={(e) => toggle(a.id, e.target.checked)}>
                <span className="mono">{a.name || a.address}</span>
              </Checkbox>
              <span className="muted agent-pick-cpus">{ok && a.cpus > 0 ? `${a.cpus} CPUs` : ""}</span>
              <span id={said} className="agent-pick-said">
                <span className="agent-pick-status">
                  <StatusDot status={a.status} />
                  {statusText(a)}
                </span>
                {!ok && a.detail && <span className="muted agent-pick-detail">{a.detail}</span>}
              </span>
            </li>
          );
        })}
      </ul>
      <p className="muted small">The plan's VUs and arrival rates are split evenly between the selected agents; results are merged here.</p>
    </div>
  );
}
