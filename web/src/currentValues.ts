import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// Current values: what a variable holds this session, as opposed to the
// initial value saved in the file. Extractors, set-variables processors and
// scripts' pm.variables write the session's runtime scope, which wins over
// every other scope, so a runtime value is what requests actually use.

export interface Current {
  /** The variable has a session value (else the initial value applies). */
  has: boolean;
  /** The value; null means unset for this session. */
  value?: string | null;
  /** A request set it (runtime scope). */
  byRequest: boolean;
}

export function useCurrentValues(scope: "globals" | "environment" | "collection", name?: string) {
  const qc = useQueryClient();
  const session = useQuery({ queryKey: ["session-vars"], queryFn: api.sessionVariables, refetchOnWindowFocus: true });
  const s = session.data;
  const own = (scope === "globals" ? s?.globals : scope === "environment" ? s?.environments?.[name!] : s?.collections?.[name!]) ?? {};
  const runtime = s?.runtime ?? {};
  const hasKey = (m: Record<string, unknown>, k: string) => k !== "" && Object.prototype.hasOwnProperty.call(m, k);

  const get = (key: string): Current => {
    if (hasKey(runtime, key)) return { has: true, value: runtime[key], byRequest: true };
    if (hasKey(own, key)) return { has: true, value: own[key], byRequest: false };
    return { has: false, byRequest: false };
  };
  const refresh = () => qc.invalidateQueries({ queryKey: ["session-vars"] });
  // Editing changes the value requests use: the runtime one when a request set it.
  const set = async (key: string, value: string | null) => {
    if (hasKey(runtime, key)) await api.setCurrentValue({ scope: "runtime", key, value });
    else await api.setCurrentValue({ scope, name, key, value });
    await refresh();
  };
  // Back to the initial value: drop both session layers.
  const reset = async (key: string) => {
    if (hasKey(runtime, key)) await api.setCurrentValue({ scope: "runtime", key, reset: true });
    if (hasKey(own, key)) await api.setCurrentValue({ scope, name, key, reset: true });
    await refresh();
  };
  return { get, set, reset };
}
