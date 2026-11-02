// {{variable}} awareness for inputs: resolve a name the way the engine does
// (runtime → environment → folders → collection → globals), colour it by
// whether it is defined, and let the user view or edit it from a popover.
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, Popover, Select, Tag } from "antd";
import type { InputProps } from "antd";
import type { TextAreaProps } from "antd/es/input";
import { api, type Variable } from "./api";
import { useApp } from "./store";
import { Eye, EyeOff } from "./icons";

export type VarScopeKind = "runtime" | "environment" | "folder" | "collection" | "globals" | "dynamic";

export interface VarInfo {
  name: string;
  value: string;
  scope: VarScopeKind;
  /** Environment name, folder or collection path. */
  where?: string;
  secret?: boolean;
  /** A session ("current") value overrides the file's. */
  current?: boolean;
}

const DYNAMIC: Record<string, string> = {
  $guid: "A random UUID",
  $randomUUID: "A random UUID",
  $timestamp: "Unix time in seconds",
  $isoTimestamp: "The current time, ISO 8601",
  $randomInt: "A random integer 0–1000",
  $randomBoolean: "true or false",
  $randomAlphaNumeric: "One random letter or digit",
  $randomHexadecimalColor: "A random #rrggbb colour",
  $randomEmail: "A random email address",
  $randomUserName: "A random user name",
  $randomPassword: "A random password",
  $randomIP: "A random IPv4 address",
};

/** The request (or collection/folder) path whose scopes apply to inputs below. */
const ScopeContext = createContext<string | null>(null);

export function VarScope({ path, children }: { path: string | null; children: React.ReactNode }) {
  return <ScopeContext.Provider value={path}>{children}</ScopeContext.Provider>;
}

function fileVars(vars: Variable[] | undefined, session: Record<string, string | null> | undefined) {
  const out = new Map<string, { value: string; secret?: boolean; current?: boolean }>();
  for (const v of vars ?? []) if (!v.disabled && v.key) out.set(v.key, { value: v.value, secret: v.secret });
  for (const [k, v] of Object.entries(session ?? {})) {
    if (v === null) out.delete(k);
    else out.set(k, { value: v, secret: out.get(k)?.secret, current: true });
  }
  return out;
}

/** Resolves variable names for the current scope (see VarScope). */
export function useVariables() {
  const path = useContext(ScopeContext);
  const env = useApp((s) => s.environment);
  const segs = path?.split("/") ?? [];
  const slug = segs[0];
  // Folder paths between the collection and the request, innermost first.
  const folders = segs.slice(1, -1).map((_, i) => segs.slice(0, i + 2).join("/")).reverse();

  const session = useQuery({ queryKey: ["session-vars"], queryFn: api.sessionVariables });
  const globals = useQuery({ queryKey: ["globals"], queryFn: api.globals });
  const envQ = useQuery({ queryKey: ["environment", env], queryFn: () => api.environment(env!), enabled: !!env, retry: false });
  const coll = useQuery({ queryKey: ["settings", slug], queryFn: () => api.collection(slug!), enabled: !!slug, retry: false });
  const folderQs = useQueries({
    queries: folders.map((f) => ({ queryKey: ["settings", f], queryFn: () => api.folder(f), retry: false })),
  });

  const layers = useMemo(() => {
    const s = session.data;
    const out: { scope: VarScopeKind; where?: string; vars: ReturnType<typeof fileVars> }[] = [];
    out.push({ scope: "runtime", vars: fileVars(undefined, s?.runtime) });
    if (env) out.push({ scope: "environment", where: env, vars: fileVars(envQ.data?.variables, s?.environments?.[env]) });
    folderQs.forEach((q, i) => out.push({ scope: "folder", where: folders[i], vars: fileVars(q.data?.variables, undefined) }));
    if (slug) out.push({ scope: "collection", where: slug, vars: fileVars(coll.data?.variables, s?.collections?.[slug]) });
    out.push({ scope: "globals", vars: fileVars(globals.data?.variables, s?.globals) });
    return out;
    // folderQs is a new array each render; its data is what matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.data, env, envQ.data, coll.data, globals.data, slug, folders.join("|"), ...folderQs.map((q) => q.data)]);

  const lookup = useCallback(
    (name: string): VarInfo | undefined => {
      if (name in DYNAMIC) return { name, value: DYNAMIC[name], scope: "dynamic" };
      for (const l of layers) {
        const v = l.vars.get(name);
        if (v) return { name, value: v.value, scope: l.scope, where: l.where, secret: v.secret, current: v.current };
      }
      return undefined;
    },
    [layers],
  );
  return { lookup, path, slug, folders, environment: env };
}

const VAR_RE = /\{\{([^{}]+)\}\}/g;

function segments(text: string) {
  const out: { text: string; name?: string }[] = [];
  let last = 0;
  for (const m of text.matchAll(VAR_RE)) {
    if (m.index! > last) out.push({ text: text.slice(last, m.index) });
    out.push({ text: m[0], name: m[1].trim() });
    last = m.index! + m[0].length;
  }
  if (last < text.length) out.push({ text: text.slice(last) });
  return out;
}

/** Text with {{vars}} coloured by state. */
function Highlighted({ text, lookup }: { text: string; lookup: (n: string) => VarInfo | undefined }) {
  return (
    <>
      {segments(text).map((s, i) => {
        if (!s.name) return <span key={i}>{s.text}</span>;
        const info = lookup(s.name);
        const state = !info ? "missing" : info.scope === "dynamic" ? "dynamic" : "defined";
        return (
          <span key={i} className={`var-token is-${state}`} data-var={s.name}>
            {s.text}
          </span>
        );
      })}
      {/* Keeps a trailing newline's height in textareas. */}
      {"​"}
    </>
  );
}

// --- popover ----------------------------------------------------------------

const SCOPE_LABEL: Record<VarScopeKind, string> = {
  runtime: "Runtime",
  environment: "Environment",
  folder: "Folder",
  collection: "Collection",
  globals: "Globals",
  dynamic: "Dynamic",
};

/** Writes a variable to a scope's file and drops any session override. */
async function saveVar(scope: "environment" | "folder" | "collection" | "globals", where: string | undefined, key: string, value: string) {
  const upsert = (vars: Variable[] | undefined) => {
    const list = [...(vars ?? [])];
    const i = list.findIndex((v) => v.key === key);
    if (i >= 0) list[i] = { ...list[i], value, disabled: false };
    else list.push({ key, value });
    return list;
  };
  if (scope === "environment") {
    const env = await api.environment(where!);
    await api.saveEnvironment(where!, { ...env, variables: upsert(env.variables) });
    await api.setCurrentValue({ scope: "environment", name: where, key, reset: true });
  } else if (scope === "globals") {
    const g = await api.globals();
    await api.saveGlobals({ ...g, variables: upsert(g.variables) });
    await api.setCurrentValue({ scope: "globals", key, reset: true });
  } else if (scope === "collection") {
    const c = await api.collection(where!);
    await api.saveCollection(where!, { ...c, variables: upsert(c.variables) });
    await api.setCurrentValue({ scope: "collection", name: where, key, reset: true });
  } else {
    const f = await api.folder(where!);
    await api.saveFolder(where!, { ...f, variables: upsert(f.variables) });
  }
}

function VarCard({ name, onDone }: { name: string; onDone: () => void }) {
  const { lookup, slug, folders, environment } = useVariables();
  const qc = useQueryClient();
  const { message } = App.useApp();
  const info = lookup(name);
  const [value, setValue] = useState(info?.scope === "dynamic" ? "" : (info?.value ?? ""));
  const [reveal, setReveal] = useState(false);
  const targets = [
    ...(environment ? [{ value: "environment", label: `Environment · ${environment}` }] : []),
    ...(folders[0] ? [{ value: "folder", label: `Folder · ${folders[0]}` }] : []),
    ...(slug ? [{ value: "collection", label: `Collection · ${slug}` }] : []),
    { value: "globals", label: "Globals" },
  ];
  const [target, setTarget] = useState(targets[0].value);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (info && info.scope !== "dynamic") setValue(info.value);
    // Reset only when the variable changes, not on every refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);

  const save = async () => {
    const scope = (info ? info.scope : target) as "environment" | "folder" | "collection" | "globals";
    const where = info ? info.where : scope === "environment" ? environment! : scope === "folder" ? folders[0] : scope === "collection" ? slug! : undefined;
    setSaving(true);
    try {
      await saveVar(scope, where, name, value);
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["session-vars"] }),
        qc.invalidateQueries({ queryKey: ["globals"] }),
        qc.invalidateQueries({ queryKey: ["environment"] }),
        qc.invalidateQueries({ queryKey: ["settings"] }),
      ]);
      message.success(`Saved {{${name}}}`);
      onDone();
    } catch (e) {
      message.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const editable = !info || (info.scope !== "dynamic" && info.scope !== "runtime");
  return (
    <div className="var-card">
      <div className="var-card-head">
        <code>{`{{${name}}}`}</code>
        {info ? (
          <Tag color={info.scope === "dynamic" ? "purple" : "green"}>
            {SCOPE_LABEL[info.scope]}
            {info.where ? ` · ${info.where}` : ""}
          </Tag>
        ) : (
          <Tag color="red">Not defined</Tag>
        )}
      </div>
      {info?.scope === "dynamic" && <p className="var-card-note">{info.value}. Generated on every send.</p>}
      {info?.scope === "runtime" && (
        <>
          <code className="var-card-value">{info.value || "(empty)"}</code>
          <p className="var-card-note">Set this session by a script, extractor or processor.</p>
        </>
      )}
      {!info && <p className="var-card-note">It will be sent as written. Add it to:</p>}
      {!info && <Select className="var-card-target" value={target} onChange={setTarget} options={targets} aria-label="Add the variable to" />}
      {editable && (
        <div className="var-card-edit">
          <Input
            className="mono"
            type={info?.secret && !reveal ? "password" : "text"}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onPressEnter={save}
            aria-label={`Value of ${name}`}
            suffix={
              info?.secret ? (
                <button type="button" className="icon-btn" onClick={() => setReveal(!reveal)} aria-label={reveal ? "Hide value" : "Show value"}>
                  {reveal ? <EyeOff size={13} /> : <Eye size={13} />}
                </button>
              ) : undefined
            }
          />
          <Button type="primary" loading={saving} onClick={save}>
            {info ? "Save" : "Add"}
          </Button>
        </div>
      )}
      {info?.current && editable && <p className="var-card-note">A session value overrides the file; saving replaces both.</p>}
    </div>
  );
}

// --- fields -------------------------------------------------------------------

/** Mirrors an input's text with highlighted {{vars}} and a hover popover. */
function useVarOverlay(text: string, multiline: boolean) {
  const { lookup } = useVariables();
  const wrap = useRef<HTMLDivElement>(null);
  const mirror = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<{ name: string; left: number; top: number; width: number; height: number } | null>(null);
  const closeTimer = useRef<number | undefined>(undefined);
  const has = text.includes("{{");

  const field = () => wrap.current?.querySelector<HTMLInputElement | HTMLTextAreaElement>("input, textarea") ?? null;

  // Match the mirror's box to the field's text box, and follow its scroll.
  const sync = useCallback(() => {
    const el = field();
    const m = mirror.current;
    const w = wrap.current;
    if (!el || !m || !w) return;
    const cs = getComputedStyle(el);
    const er = el.getBoundingClientRect();
    const wr = w.getBoundingClientRect();
    Object.assign(m.style, {
      left: `${er.left - wr.left}px`,
      top: `${er.top - wr.top}px`,
      width: `${er.width}px`,
      height: `${er.height}px`,
      padding: cs.padding,
      borderWidth: cs.borderWidth,
      fontFamily: cs.fontFamily,
      fontSize: cs.fontSize,
      fontWeight: cs.fontWeight,
      fontStyle: cs.fontStyle,
      letterSpacing: cs.letterSpacing,
      wordSpacing: cs.wordSpacing,
      tabSize: cs.tabSize,
      lineHeight: cs.lineHeight,
      textAlign: cs.textAlign,
    });
    const inner = m.firstElementChild as HTMLElement | null;
    if (inner) inner.style.transform = `translate(${-el.scrollLeft}px, ${-el.scrollTop}px)`;
  }, []);

  useLayoutEffect(() => {
    if (!has) return;
    sync();
    const el = field();
    if (!el) return;
    const ro = new ResizeObserver(sync);
    ro.observe(el);
    el.addEventListener("scroll", sync);
    el.addEventListener("input", sync);
    el.addEventListener("keyup", sync);
    el.addEventListener("select", sync);
    return () => {
      ro.disconnect();
      el.removeEventListener("scroll", sync);
      el.removeEventListener("input", sync);
      el.removeEventListener("keyup", sync);
      el.removeEventListener("select", sync);
    };
  });

  const scheduleClose = () => {
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setHover(null), 250);
  };
  const keepOpen = () => window.clearTimeout(closeTimer.current);

  const onMouseMove = (e: React.MouseEvent) => {
    if (!has || !mirror.current || !wrap.current) return;
    const wr = wrap.current.getBoundingClientRect();
    for (const t of mirror.current.querySelectorAll<HTMLElement>("[data-var]")) {
      for (const r of t.getClientRects()) {
        if (e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom) {
          keepOpen();
          const name = t.dataset.var!;
          if (hover?.name !== name || Math.abs(hover.left - (r.left - wr.left)) > 1) {
            setHover({ name, left: r.left - wr.left, top: r.top - wr.top, width: r.width, height: r.height });
          }
          return;
        }
      }
    }
    if (hover) scheduleClose();
  };

  const overlay = has ? (
    <>
      <div ref={mirror} className={`var-mirror ${multiline ? "is-multi" : ""}`} aria-hidden>
        <div className="var-mirror-text">
          <Highlighted text={text} lookup={lookup} />
        </div>
      </div>
      <Popover
        open={!!hover}
        placement="bottomLeft"
        arrow={false}
        destroyOnHidden
        content={
          hover && (
            <div onMouseEnter={keepOpen} onMouseLeave={scheduleClose}>
              <VarCard key={hover.name} name={hover.name} onDone={() => setHover(null)} />
            </div>
          )
        }
      >
        <span
          className="var-anchor"
          style={hover ? { left: hover.left, top: hover.top, width: hover.width, height: hover.height } : { display: "none" }}
        />
      </Popover>
    </>
  ) : null;

  return {
    wrapProps: {
      ref: wrap,
      className: `var-field ${has ? "has-vars" : ""} ${multiline ? "is-multi" : ""}`,
      onMouseMove,
      onMouseLeave: () => hover && scheduleClose(),
    },
    overlay,
  };
}

/** antd Input that highlights {{variables}} and explains them on hover. */
export function VarInput(props: InputProps & { value?: string }) {
  const { wrapProps, overlay } = useVarOverlay(String(props.value ?? ""), false);
  return (
    <div {...wrapProps}>
      {overlay}
      <Input {...props} />
    </div>
  );
}

/** antd TextArea that highlights {{variables}} and explains them on hover. */
export function VarTextArea(props: TextAreaProps & { value?: string }) {
  const { wrapProps, overlay } = useVarOverlay(String(props.value ?? ""), true);
  return (
    <div {...wrapProps}>
      {overlay}
      <Input.TextArea {...props} />
    </div>
  );
}
