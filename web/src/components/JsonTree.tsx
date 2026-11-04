import { useState } from "react";
import { ChevronRight } from "../icons";

// A collapsible JSON view for response bodies: every object and array can
// be folded, and folded ones say how much they hold. Children render only
// when expanded, so large bodies stay quick.

type Json = null | boolean | number | string | Json[] | { [k: string]: Json };

/** How many levels start open: all of a small body, the top of a big one. */
export function defaultDepth(value: unknown): number {
  let nodes = 0;
  const count = (v: unknown, depth: number): void => {
    if (nodes > 1500 || depth > 50) return;
    nodes++;
    if (v && typeof v === "object") for (const c of Object.values(v)) count(c, depth + 1);
  };
  count(value, 0);
  return nodes <= 1500 ? Infinity : 2;
}

export function JsonTree({ value, depth }: { value: unknown; depth: number }) {
  return (
    <div className="json-tree mono" role="tree" aria-label="Response body">
      <Node value={value as Json} level={0} open={depth} last />
    </div>
  );
}

function Scalar({ v }: { v: Json }) {
  if (v === null) return <span className="j-null">null</span>;
  switch (typeof v) {
    case "string":
      return <span className="j-str">{JSON.stringify(v)}</span>;
    case "number":
      return <span className="j-num">{String(v)}</span>;
    case "boolean":
      return <span className="j-bool">{String(v)}</span>;
  }
  return null;
}

function Node({ name, value, level, open, last }: { name?: string; value: Json; level: number; open: number; last: boolean }) {
  const isObj = value !== null && typeof value === "object";
  const [expanded, setExpanded] = useState(level < open);
  const comma = last ? "" : ",";
  const label = name !== undefined && (
    <>
      <span className="j-key">{JSON.stringify(name)}</span>
      <span className="j-punct">: </span>
    </>
  );
  if (!isObj) {
    return (
      <div className="j-row" role="treeitem" aria-level={level + 1} style={{ paddingLeft: level * 16 + 18 }}>
        {label}
        <Scalar v={value} />
        <span className="j-punct">{comma}</span>
      </div>
    );
  }
  const isArr = Array.isArray(value);
  const entries: [string, Json][] = isArr ? (value as Json[]).map((v, i) => [String(i), v]) : Object.entries(value as Record<string, Json>);
  const [o, c] = isArr ? ["[", "]"] : ["{", "}"];
  const count = `${entries.length} ${isArr ? (entries.length === 1 ? "item" : "items") : entries.length === 1 ? "key" : "keys"}`;
  if (entries.length === 0) {
    return (
      <div className="j-row" role="treeitem" aria-level={level + 1} style={{ paddingLeft: level * 16 + 18 }}>
        {label}
        <span className="j-punct">
          {o}
          {c}
          {comma}
        </span>
      </div>
    );
  }
  const what = name !== undefined ? (isArr ? `array ${name}` : `object ${name}`) : isArr ? "array" : "object";
  return (
    <div role="treeitem" aria-level={level + 1} aria-expanded={expanded}>
      <div className="j-row" style={{ paddingLeft: level * 16 }}>
        <button className="j-toggle" onClick={() => setExpanded(!expanded)} aria-label={`${expanded ? "Collapse" : "Expand"} ${what}`}>
          <ChevronRight size={12} className={expanded ? "open" : undefined} />
        </button>
        {label}
        <span className="j-punct">{o}</span>
        {!expanded && (
          <>
            <button className="j-folded" onClick={() => setExpanded(true)} tabIndex={-1} aria-hidden>
              …
            </button>
            <span className="j-punct">
              {c}
              {comma}
            </span>
            <span className="j-count">{count}</span>
          </>
        )}
      </div>
      {expanded && (
        <>
          <div role="group">
            {entries.map(([k, v], i) => (
              <Node key={k} name={isArr ? undefined : k} value={v} level={level + 1} open={open} last={i === entries.length - 1} />
            ))}
          </div>
          <div className="j-row" style={{ paddingLeft: level * 16 + 18 }}>
            <span className="j-punct">
              {c}
              {comma}
            </span>
          </div>
        </>
      )}
    </div>
  );
}
