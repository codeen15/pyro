// The user guide (docs/guide/*.md), bundled into the app and rendered here.
import { useEffect, useMemo, useRef, useState } from "react";
import { Input } from "antd";
import { Marked } from "marked";
import { useApp } from "../store";
import { Search } from "../icons";

const files = import.meta.glob("../../../docs/guide/*.md", { query: "?raw", import: "default", eager: true }) as Record<string, string>;

/** Page order, as in the guide's README. */
const ORDER = ["README", "getting-started", "requests", "variables", "realtime", "processors", "runner", "load-testing", "distributed", "import-export", "plugins", "cli", "workspace", "tips"];

const REPO = "https://github.com/codeen15/pyro/blob/main/docs/";

interface Page {
  id: string;
  title: string;
  text: string;
}

const pages: Page[] = Object.entries(files)
  .map(([path, text]) => {
    const id = path.split("/").pop()!.replace(/\.md$/, "");
    const title = id === "README" ? "Overview" : (/^#\s+(.+)$/m.exec(text)?.[1] ?? id);
    return { id, title, text };
  })
  .sort((a, b) => (ORDER.indexOf(a.id) + 1 || 99) - (ORDER.indexOf(b.id) + 1 || 99));

/** GitHub-style heading anchors, so links like page.md#some-heading work in both places. */
export function slug(text: string) {
  return text
    .toLowerCase()
    .replace(/<[^>]+>/g, "")
    .replace(/[^\p{L}\p{N}\s-]/gu, "")
    .trim()
    .replace(/\s/g, "-");
}

const md = new Marked({
  gfm: true,
  renderer: {
    heading({ tokens, depth }) {
      const html = this.parser.parseInline(tokens);
      return `<h${depth} id="${slug(html)}">${html}</h${depth}>\n`;
    },
  },
});

export function DocsPage({ target }: { target: string }) {
  const select = useApp((s) => s.select);
  const [pageId, anchor] = target.split("#");
  const page = pages.find((p) => p.id === (pageId || "README")) ?? pages[0];
  const html = useMemo(() => md.parse(page.text) as string, [page]);
  const [q, setQ] = useState("");
  const body = useRef<HTMLDivElement>(null);

  const matches = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return null;
    return pages
      .map((p) => {
        const i = p.text.toLowerCase().indexOf(s);
        if (i < 0) return null;
        const start = Math.max(0, i - 40);
        const snippet = p.text.slice(start, i + s.length + 60).replace(/[#*`|>[\]]/g, "").replace(/\s+/g, " ").trim();
        return { page: p, snippet };
      })
      .filter((m): m is { page: Page; snippet: string } => m !== null);
  }, [q]);

  useEffect(() => {
    const scroller = body.current?.closest(".page");
    if (anchor) document.getElementById(anchor)?.scrollIntoView({ block: "start" });
    else scroller?.scrollTo({ top: 0 });
  }, [page.id, anchor]);

  // Links: guide pages open here, reference docs on GitHub, the web in a new tab.
  const onClick = (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest("a");
    const href = a?.getAttribute("href");
    if (!a || !href) return;
    if (/^https?:/.test(href)) {
      e.preventDefault();
      window.open(href, "_blank", "noopener");
      return;
    }
    e.preventDefault();
    if (href.startsWith("#")) {
      select(`docs:${page.id}${href}`);
      document.getElementById(href.slice(1))?.scrollIntoView({ block: "start" });
      return;
    }
    const [file, hash] = href.split("#");
    if (file.startsWith("../")) {
      window.open(REPO + file.replace(/^\.\.\//, "") + (hash ? `#${hash}` : ""), "_blank", "noopener");
      return;
    }
    const id = file.replace(/^\.\//, "").replace(/\.md$/, "");
    if (pages.some((p) => p.id === id)) select(`docs:${id}${hash ? `#${hash}` : ""}`);
  };

  return (
    <div className="page docs-page">
      <nav className="card docs-nav" aria-label="Guide pages">
        <Input allowClear prefix={<Search size={14} />} placeholder="Search the guide" aria-label="Search the guide" value={q} onChange={(e) => setQ(e.target.value)} />
        {matches ? (
          <ul className="docs-results">
            {matches.length === 0 && <li className="muted small">Nothing found.</li>}
            {matches.map((m) => (
              <li key={m.page.id}>
                <button
                  className={`row ${m.page.id === page.id ? "is-active" : ""}`}
                  onClick={() => {
                    select(`docs:${m.page.id}`);
                  }}
                >
                  <span className="row-label">{m.page.title}</span>
                </button>
                <p className="docs-snippet">…{m.snippet}…</p>
              </li>
            ))}
          </ul>
        ) : (
          <ul className="docs-toc">
            {pages.map((p) => (
              <li key={p.id}>
                <button className={`row ${p.id === page.id ? "is-active" : ""}`} onClick={() => select(`docs:${p.id}`)} aria-current={p.id === page.id ? "page" : undefined}>
                  <span className="row-label">{p.title}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </nav>
      <article className="card docs-body" ref={body} onClick={onClick} dangerouslySetInnerHTML={{ __html: html }} />
    </div>
  );
}
