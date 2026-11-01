import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Cookie, Plus, Trash2 } from "lucide-react";
import { api, type StoredCookie } from "../api";

// The interactive cookie jar: what servers set while you send requests from
// the UI (kept while pyro serve runs), editable like Postman's cookie manager.

export function CookiesPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["cookies"], queryFn: api.cookies, refetchOnWindowFocus: true });
  const [draft, setDraft] = useState({ domain: "", name: "", value: "", path: "/" });
  const [error, setError] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["cookies"] });

  const byDomain = new Map<string, StoredCookie[]>();
  for (const c of q.data ?? []) byDomain.set(c.domain, [...(byDomain.get(c.domain) ?? []), c]);

  const add = async () => {
    setError(null);
    try {
      await api.putCookie({ ...draft, domain: draft.domain.trim(), name: draft.name.trim() });
      setDraft({ ...draft, name: "", value: "" });
      refresh();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <section className="loadtest cookies-page">
      <header className="lt-head">
        <div>
          <h1>Cookies</h1>
          <p className="muted small">Set by servers while you send requests here, and sent back to them automatically. Kept in memory while Pyro runs; collection runs and load tests use their own.</p>
        </div>
        <div className="lt-actions">
          <button
            className="btn ghost"
            disabled={!q.data?.length}
            onClick={async () => {
              if (confirm("Delete every cookie?")) {
                await api.deleteCookies();
                refresh();
              }
            }}
          >
            <Trash2 size={13} /> Clear all
          </button>
        </div>
      </header>

      <form
        className="cookie-add"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <input className="cell mono" value={draft.domain} onChange={(e) => setDraft({ ...draft, domain: e.target.value })} placeholder="domain (api.example.com)" aria-label="Domain" />
        <input className="cell mono" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="name" aria-label="Cookie name" />
        <input className="cell mono" value={draft.value} onChange={(e) => setDraft({ ...draft, value: e.target.value })} placeholder="value" aria-label="Cookie value" />
        <input className="cell mono" value={draft.path} onChange={(e) => setDraft({ ...draft, path: e.target.value })} placeholder="/" aria-label="Path" />
        <button className="btn ghost small" type="submit" disabled={!draft.domain.trim() || !draft.name.trim()}>
          <Plus size={13} /> Add cookie
        </button>
      </form>
      {error && <p className="inline-error">{error}</p>}

      {q.data?.length === 0 && (
        <p className="empty-note muted">
          <Cookie size={16} aria-hidden /> No cookies yet. Responses with <code>Set-Cookie</code> fill this in.
        </p>
      )}
      {[...byDomain].map(([domain, cookies]) => (
        <div key={domain} className="cookie-domain">
          <h2 className="sub-head">
            <span className="mono">{domain}</span>
            <button
              className="icon-btn"
              aria-label={`Delete cookies for ${domain}`}
              title="Delete this domain's cookies"
              onClick={async () => {
                await api.deleteCookies({ domain });
                refresh();
              }}
            >
              <Trash2 size={13} />
            </button>
          </h2>
          <div className="scroll-x">
            <table className="data-table cookie-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Value</th>
                  <th>Path</th>
                  <th>Expires</th>
                  <th>Flags</th>
                  <th className="pick-col">
                    <span className="sr-only">Delete</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {cookies.map((c) => (
                  <tr key={c.path + c.name}>
                    <td className="mono">{c.name}</td>
                    <td>
                      <CookieValue c={c} onSaved={refresh} />
                    </td>
                    <td className="mono">{c.path}</td>
                    <td>{c.expires ? new Date(c.expires).toLocaleString() : <span className="muted">session</span>}</td>
                    <td className="muted small">{[c.hostOnly ? "host only" : "subdomains", c.secure && "Secure", c.httpOnly && "HttpOnly", c.sameSite && `SameSite=${c.sameSite}`].filter(Boolean).join(" · ")}</td>
                    <td className="pick-col">
                      <button
                        className="icon-btn"
                        aria-label={`Delete cookie ${c.name}`}
                        onClick={async () => {
                          await api.deleteCookies({ domain: c.domain, path: c.path, name: c.name });
                          refresh();
                        }}
                      >
                        <Trash2 size={13} />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ))}
    </section>
  );
}

function CookieValue({ c, onSaved }: { c: StoredCookie; onSaved: () => void }) {
  const [v, setV] = useState(c.value);
  return (
    <input
      className="cell mono wide"
      value={v}
      onChange={(e) => setV(e.target.value)}
      onBlur={async () => {
        if (v !== c.value) {
          await api.putCookie({ ...c, value: v });
          onSaved();
        }
      }}
      onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()}
      aria-label={`Value of ${c.name}`}
    />
  );
}
