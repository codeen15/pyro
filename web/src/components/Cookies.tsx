import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App as AntApp, Button, Empty, Input, Table, Tooltip } from "antd";
import { Plus, Trash2 } from "../icons";
import { api, type StoredCookie } from "../api";

// The interactive cookie jar: what servers set while you send requests from
// the UI (kept while pyro serve runs), editable like Postman's cookie manager.

export function CookiesPage() {
  const qc = useQueryClient();
  const { modal } = AntApp.useApp();
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
    <div className="page">
      <section className="card cookies-page">
        <header className="page-head">
          <div>
            <h1>Cookies</h1>
            <p className="muted small">
              Set by servers while you send requests here, and sent back to them automatically. Saved for this workspace on this machine (not in the workspace
              files); collection runs and load tests start with empty jars.
            </p>
          </div>
          <div className="page-actions">
            <Button
              danger
              icon={<Trash2 size={13} />}
              disabled={!q.data?.length}
              onClick={() =>
                modal.confirm({
                  title: "Delete every cookie?",
                  okText: "Delete all",
                  okButtonProps: { danger: true },
                  onOk: async () => {
                    await api.deleteCookies();
                    refresh();
                  },
                })
              }
            >
              Clear all
            </Button>
          </div>
        </header>

        <form
          className="cookie-add"
          onSubmit={(e) => {
            e.preventDefault();
            add();
          }}
        >
          <Input className="mono" value={draft.domain} onChange={(e) => setDraft({ ...draft, domain: e.target.value })} placeholder="domain (api.example.com)" aria-label="Domain" />
          <Input className="mono" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="name" aria-label="Cookie name" />
          <Input className="mono" value={draft.value} onChange={(e) => setDraft({ ...draft, value: e.target.value })} placeholder="value" aria-label="Cookie value" />
          <Input className="mono" value={draft.path} onChange={(e) => setDraft({ ...draft, path: e.target.value })} placeholder="/" aria-label="Path" />
          <Button htmlType="submit" icon={<Plus size={13} />} disabled={!draft.domain.trim() || !draft.name.trim()}>
            Add cookie
          </Button>
        </form>
        {error && <Alert type="error" showIcon title={error} />}

        {q.data?.length === 0 && (
          <Empty
            className="cookies-empty"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={
              <>
                No cookies yet. Responses with <code>Set-Cookie</code> fill this in.
              </>
            }
          />
        )}
        {[...byDomain].map(([domain, cookies]) => (
          <div key={domain} className="cookie-domain">
            <h2 className="cookie-domain-head">
              <span className="mono">{domain}</span>
              <Tooltip title="Delete this domain's cookies">
                <Button
                  type="text"
                  icon={<Trash2 size={13} />}
                  aria-label={`Delete cookies for ${domain}`}
                  onClick={async () => {
                    await api.deleteCookies({ domain });
                    refresh();
                  }}
                />
              </Tooltip>
            </h2>
            <Table<StoredCookie>
              className="cookie-table"
              size="small"
              bordered
              pagination={false}
              scroll={{ x: "max-content" }}
              rowKey={(c) => c.path + c.name}
              dataSource={cookies}
              columns={[
                { title: "Name", key: "name", className: "mono", render: (_, c) => c.name },
                { title: "Value", key: "value", className: "cookie-value", render: (_, c) => <CookieValue c={c} onSaved={refresh} /> },
                { title: "Path", key: "path", className: "mono", render: (_, c) => c.path },
                { title: "Expires", key: "expires", render: (_, c) => (c.expires ? new Date(c.expires).toLocaleString() : <span className="muted">session</span>) },
                {
                  title: "Flags",
                  key: "flags",
                  className: "muted small",
                  render: (_, c) => [c.hostOnly ? "host only" : "subdomains", c.secure && "Secure", c.httpOnly && "HttpOnly", c.sameSite && `SameSite=${c.sameSite}`].filter(Boolean).join(" · "),
                },
                {
                  title: <span className="sr-only">Delete</span>,
                  key: "delete",
                  width: 48,
                  render: (_, c) => (
                    <Button
                      type="text"
                      icon={<Trash2 size={13} />}
                      aria-label={`Delete cookie ${c.name}`}
                      onClick={async () => {
                        await api.deleteCookies({ domain: c.domain, path: c.path, name: c.name });
                        refresh();
                      }}
                    />
                  ),
                },
              ]}
            />
          </div>
        ))}
      </section>
    </div>
  );
}

function CookieValue({ c, onSaved }: { c: StoredCookie; onSaved: () => void }) {
  const [v, setV] = useState(c.value);
  return (
    <Input
      size="small"
      className="mono"
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
