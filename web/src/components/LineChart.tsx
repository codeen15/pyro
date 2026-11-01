import { useLayoutEffect, useMemo, useRef, useState } from "react";

// Line chart per the dataviz spec: hairline solid grid, 2px lines, a 10% area
// wash for single-series charts, dashed lines only for thresholds, and a
// crosshair that snaps to the nearest sample with a readout of every series.
// Colours come from CSS tokens validated with the palette checks.

export interface Series {
  name: string;
  color: string; // a CSS colour, e.g. "var(--viz-series)"
  values: number[];
}

export interface Ref {
  label: string;
  value: number;
}

export type Unit = "" | "ms" | "%" | "/s";

const PAD = { l: 48, r: 10, t: 10, b: 24 };

export function fmtValue(v: number, unit: Unit): string {
  if (!Number.isFinite(v)) return "–";
  switch (unit) {
    case "%":
      return `${trim((v * 100).toFixed(v < 0.1 ? 2 : 1))}%`;
    case "ms":
      return v >= 1000 ? `${trim((v / 1000).toFixed(2))} s` : `${trim(v.toFixed(v < 10 ? 1 : 0))} ms`;
    case "/s":
      return v >= 1000 ? `${trim((v / 1000).toFixed(1))}K/s` : `${trim(v.toFixed(1))}/s`;
  }
  return v >= 1000 ? `${trim((v / 1000).toFixed(1))}K` : trim(v.toFixed(v < 10 && v % 1 ? 1 : 0));
}

function trim(s: string) {
  return s.includes(".") ? s.replace(/\.?0+$/, "") : s;
}

export function clock(secs: number) {
  const s = Math.round(secs);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

/** Time-axis ticks on whole, round steps (1s, 5s, 1m, …), about 4–6 of them. */
function timeTicks(maxX: number): number[] {
  const steps = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 14400];
  const step = steps.find((s) => maxX / s <= 5) ?? steps[steps.length - 1];
  const out: number[] = [];
  for (let t = 0; t <= maxX + 1e-9; t += step) out.push(t);
  return out;
}

function niceMax(v: number) {
  if (v <= 0) return 1;
  const exp = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * exp >= v) return m * exp;
  return 10 * exp;
}

export function LineChart({
  title,
  x,
  series,
  unit,
  refs = [],
  height = 180,
}: {
  title: string;
  x: number[];
  series: Series[];
  unit: Unit;
  refs?: Ref[];
  height?: number;
}) {
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(480);
  const [hover, setHover] = useState<number | null>(null);

  useLayoutEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(240, e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const plotW = width - PAD.l - PAD.r;
  const plotH = height - PAD.t - PAD.b;
  const { maxX, maxY } = useMemo(() => {
    let my = 0;
    for (const s of series) for (const v of s.values) if (Number.isFinite(v)) my = Math.max(my, v);
    for (const r of refs) my = Math.max(my, r.value);
    if (unit === "%") my = Math.max(my, 0.01);
    return { maxX: Math.max(x[x.length - 1] ?? 1, 1), maxY: niceMax(my * 1.05) };
  }, [x, series, refs, unit]);

  const px = (v: number) => PAD.l + (v / maxX) * plotW;
  const py = (v: number) => PAD.t + plotH - (v / maxY) * plotH;

  const paths = useMemo(
    () =>
      // Non-finite values are gaps (e.g. the shorter of two compared runs).
      series.map((s) => {
        let pen = "M";
        const parts: string[] = [];
        s.values.forEach((v, i) => {
          if (!Number.isFinite(v) || i >= x.length) {
            pen = "M";
            return;
          }
          parts.push(`${pen}${px(x[i]).toFixed(1)} ${py(v).toFixed(1)}`);
          pen = "L";
        });
        return parts.join(" ");
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [series, x, width, maxX, maxY],
  );

  const nearest = (clientX: number) => {
    const el = wrap.current;
    if (!el || x.length === 0) return null;
    const r = el.getBoundingClientRect();
    const t = ((clientX - r.left - PAD.l) / plotW) * maxX;
    let best = 0;
    for (let i = 1; i < x.length; i++) if (Math.abs(x[i] - t) < Math.abs(x[best] - t)) best = i;
    return best;
  };

  const ticksY = [0, 1, 2, 3, 4].map((i) => (maxY * i) / 4);
  const ticksX = timeTicks(maxX);
  const hi = hover !== null && hover < x.length ? hover : null;
  const tipLeft = hi !== null ? px(x[hi]) : 0;

  return (
    <figure className="viz">
      <figcaption>
        <span className="viz-title">{title}</span>
        {series.length > 1 && (
          <span className="viz-legend">
            {series.map((s) => (
              <span key={s.name}>
                <i style={{ background: s.color }} />
                {s.name}
              </span>
            ))}
          </span>
        )}
      </figcaption>
      <div
        ref={wrap}
        className="viz-plot"
        tabIndex={0}
        role="img"
        aria-label={`${title}. Use left and right arrow keys to read values.`}
        onPointerMove={(e) => setHover(nearest(e.clientX))}
        onPointerLeave={() => setHover(null)}
        onFocus={() => setHover(x.length ? x.length - 1 : null)}
        onBlur={() => setHover(null)}
        onKeyDown={(e) => {
          if (!x.length) return;
          if (e.key === "ArrowLeft") setHover((h) => Math.max(0, (h ?? x.length - 1) - 1));
          if (e.key === "ArrowRight") setHover((h) => Math.min(x.length - 1, (h ?? 0) + 1));
        }}
      >
        <svg width={width} height={height} aria-hidden>
          {ticksY.map((v) => (
            <g key={v}>
              <line className="viz-grid" x1={PAD.l} x2={PAD.l + plotW} y1={py(v)} y2={py(v)} />
              <text className="viz-tick" x={PAD.l - 6} y={py(v) + 3.5} textAnchor="end">
                {fmtValue(v, unit === "/s" ? "" : unit)}
              </text>
            </g>
          ))}
          {ticksX.map((v, i) => (
            <text key={i} className="viz-tick" x={px(v)} y={height - 7} textAnchor={i === 0 ? "start" : px(v) > PAD.l + plotW - 20 ? "end" : "middle"}>
              {clock(v)}
            </text>
          ))}
          {refs.map((r) => (
            <g key={r.label}>
              <line className="viz-ref" x1={PAD.l} x2={PAD.l + plotW} y1={py(r.value)} y2={py(r.value)} />
              <text className="viz-ref-label" x={PAD.l + plotW} y={py(r.value) - 4} textAnchor="end">
                {r.label}
              </text>
            </g>
          ))}
          {x.length > 1 &&
            series.length === 1 &&
            paths[0] && <path d={`${paths[0]} L${px(x[x.length - 1])} ${py(0)} L${px(x[0])} ${py(0)} Z`} fill={series[0].color} fillOpacity={0.1} />}
          {series.map((s, i) => (
            <path key={s.name} d={paths[i]} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
          ))}
          {hi !== null && (
            <>
              <line className="viz-cross" x1={tipLeft} x2={tipLeft} y1={PAD.t} y2={PAD.t + plotH} />
              {series.map((s) =>
                Number.isFinite(s.values[hi]) ? (
                  <circle key={s.name} cx={tipLeft} cy={py(s.values[hi])} r={4} fill={s.color} stroke="var(--viz-surface)" strokeWidth={2} />
                ) : null,
              )}
            </>
          )}
        </svg>
        {x.length === 0 && <p className="viz-empty">Waiting for the first interval…</p>}
        {hi !== null && (
          <div className="viz-tip" style={{ left: Math.min(tipLeft + 12, width - 140) }}>
            <b>{clock(x[hi])}</b>
            {series.map((s) => (
              <div key={s.name}>
                <i style={{ background: s.color }} />
                <strong>{fmtValue(s.values[hi] ?? NaN, unit)}</strong>
                {series.length > 1 && <span>{s.name}</span>}
              </div>
            ))}
          </div>
        )}
      </div>
    </figure>
  );
}
