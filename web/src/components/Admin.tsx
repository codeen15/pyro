import { useState } from "react";
import { Alert, App as AntApp, Button, Descriptions, Input, Modal, Select, Space, Switch, Table, Tabs, Tag, Tooltip } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, KeyRound, Plus, Trash2 } from "../icons";
import { team, type APIToken, type TeamUser } from "../api";
import { useTeam } from "../team";

// Server administration (users, settings) and personal API tokens.

const PROVIDER_LABEL: Record<string, string> = { local: "Password", ldap: "LDAP", oidc: "SSO" };

function CopyField({ label, value }: { label: string; value: string }) {
  const { message } = AntApp.useApp();
  return (
    <Space.Compact block>
      <Input readOnly value={value} aria-label={label} onFocus={(e) => e.target.select()} />
      <Button
        icon={<Copy size={14} />}
        onClick={() => navigator.clipboard.writeText(value).then(() => message.success("Copied"), () => message.error("Couldn't copy; select it and copy"))}
      >
        Copy
      </Button>
    </Space.Compact>
  );
}

export function AdminPage() {
  const me = useTeam((s) => s.me);
  const qc = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const users = useQuery({ queryKey: ["admin-users"], queryFn: team.adminUsers, enabled: !!me?.user.admin });
  const settings = useQuery({ queryKey: ["admin-settings"], queryFn: team.adminSettings, enabled: !!me?.user.admin });
  const [search, setSearch] = useState("");
  const [link, setLink] = useState<{ email: string; url: string; emailed: boolean } | null>(null);

  if (!me?.user.admin) {
    return (
      <div className="page">
        <section className="card projects-page">
          <p>Only server administrators can open this page.</p>
        </section>
      </div>
    );
  }

  const update = async (u: TeamUser, b: { admin?: boolean; disabled?: boolean }, done: string) => {
    try {
      await team.adminUpdateUser(u.id, b);
      message.success(done);
      qc.invalidateQueries({ queryKey: ["admin-users"] });
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const q = search.trim().toLowerCase();
  const rows = (users.data ?? []).filter((u) => !q || u.name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q));
  const s = settings.data;

  return (
    <div className="page">
      <section className="card projects-page admin-page">
        <header className="page-head">
          <div>
            <h1>Administration</h1>
            <p className="muted small">Accounts and settings for this Pyro team server.</p>
          </div>
        </header>
        <Tabs
          items={[
            {
              key: "users",
              label: `Users${users.data ? ` (${users.data.length})` : ""}`,
              children: (
                <>
                  <Input allowClear className="admin-search" placeholder="Search by name or email" aria-label="Search users" value={search} onChange={(e) => setSearch(e.target.value)} />
                  <Table<TeamUser>
                    rowKey="id"
                    size="small"
                    pagination={{ pageSize: 50, hideOnSinglePage: true }}
                    scroll={{ x: "max-content" }}
                    loading={users.isPending}
                    dataSource={rows}
                    columns={[
                      {
                        title: "Name",
                        render: (_, u) => (
                          <span className="member-name">
                            <strong>{u.name}</strong>
                            {u.id === me.user.id && <span className="muted small"> (you)</span>}
                            <span className="muted small member-email">{u.email}</span>
                          </span>
                        ),
                      },
                      { title: "Sign-in", width: 100, render: (_, u) => <Tag>{PROVIDER_LABEL[u.provider] ?? u.provider}</Tag> },
                      {
                        title: "Administrator",
                        width: 130,
                        render: (_, u) => (
                          <Switch
                            aria-label={`Administrator: ${u.name}`}
                            checked={u.admin}
                            onChange={(on) => update(u, { admin: on }, on ? `${u.name} is now an administrator` : `${u.name} is no longer an administrator`)}
                          />
                        ),
                      },
                      {
                        title: "Active",
                        width: 90,
                        render: (_, u) => (
                          <Switch
                            aria-label={`Active: ${u.name}`}
                            checked={!u.disabled}
                            onChange={(on) =>
                              on
                                ? update(u, { disabled: false }, `${u.name} can sign in again`)
                                : modal.confirm({
                                    title: `Disable ${u.name}?`,
                                    content: "They're signed out everywhere at once and can't sign in. Their API tokens stop working. Workspaces and work are kept.",
                                    okText: "Disable",
                                    okButtonProps: { danger: true },
                                    onOk: () => update(u, { disabled: true }, `${u.name} is disabled`),
                                  })
                            }
                          />
                        ),
                      },
                      { title: "Joined", width: 120, render: (_, u) => (u.created ? new Date(u.created).toLocaleDateString() : "") },
                      {
                        title: <span className="sr-only">Actions</span>,
                        width: 150,
                        align: "right",
                        render: (_, u) =>
                          u.provider === "local" ? (
                            <Button
                              onClick={async () => {
                                try {
                                  const r = await team.adminResetLink(u.id);
                                  setLink({ email: u.email, url: r.url, emailed: r.emailed });
                                } catch (e) {
                                  message.error((e as Error).message);
                                }
                              }}
                            >
                              Reset password
                            </Button>
                          ) : (
                            <Tooltip title={`Passwords are managed by ${PROVIDER_LABEL[u.provider]}`}>
                              <span className="muted small">Managed elsewhere</span>
                            </Tooltip>
                          ),
                      },
                    ]}
                  />
                </>
              ),
            },
            {
              key: "server",
              label: "Server",
              children: s ? (
                <Descriptions column={1} size="small" bordered className="admin-settings">
                  <Descriptions.Item label="Version">{s.version}</Descriptions.Item>
                  <Descriptions.Item label="Public address">{s.publicURL || <span className="muted">not set (uses the address in the browser)</span>}</Descriptions.Item>
                  <Descriptions.Item label="Database">{s.database}</Descriptions.Item>
                  <Descriptions.Item label="Sign-up">
                    {s.signupPolicy === "domains" ? `Email domains: ${s.signupDomains?.join(", ")}` : s.signupPolicy === "open" ? "Open to anyone" : "By invitation"}
                  </Descriptions.Item>
                  <Descriptions.Item label="LDAP / Active Directory">{s.ldap || <span className="muted">off</span>}</Descriptions.Item>
                  <Descriptions.Item label="Single sign-on">
                    {s.oidc.length ? s.oidc.map((p) => `${p.name} (${p.issuer})`).join("; ") : <span className="muted">off</span>}
                  </Descriptions.Item>
                  <Descriptions.Item label="Invite and reset emails">{s.email ? "On" : <span className="muted">off (links are copied by hand)</span>}</Descriptions.Item>
                  <Descriptions.Item label="Load tests at once">{s.maxLoads}</Descriptions.Item>
                </Descriptions>
              ) : null,
            },
          ]}
        />
        <p className="muted small">These settings come from server.yaml, environment variables or flags; change them there and restart Pyro.</p>
      </section>
      <Modal open={!!link} title={`Password link for ${link?.email ?? ""}`} onCancel={() => setLink(null)} footer={<Button type="primary" onClick={() => setLink(null)}>Done</Button>}>
        {link && (
          <div className="invite-result">
            {link.emailed && <Alert type="success" showIcon message={`Emailed to ${link.email}.`} />}
            <label className="modal-field">
              <span>One-time link</span>
              <CopyField label="Password link" value={link.url} />
              <span className="muted small">It works once, for 24 hours. Send it to them privately.</span>
            </label>
          </div>
        )}
      </Modal>
    </div>
  );
}

/** Personal access tokens, on the Account page. */
export function APITokens() {
  const qc = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const tokens = useQuery({ queryKey: ["api-tokens"], queryFn: team.apiTokens });
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [days, setDays] = useState(90);
  const [made, setMade] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["api-tokens"] });
  const close = () => {
    setCreating(false);
    setMade(null);
    setName("");
  };

  return (
    <div className="account-form api-tokens">
      <h2>API tokens</h2>
      <p className="muted small">
        For scripts, CI and the <code>pyro</code> command line: send <code>Authorization: Bearer &lt;token&gt;</code>. A token can do anything you can.
      </p>
      <Table<APIToken>
        rowKey="id"
        size="small"
        pagination={false}
        scroll={{ x: "max-content" }}
        loading={tokens.isPending}
        locale={{ emptyText: "No tokens." }}
        dataSource={tokens.data ?? []}
        columns={[
          { title: "Name", dataIndex: "name" },
          { title: "Last used", render: (_, t) => (t.lastUsed ? new Date(t.lastUsed).toLocaleString() : <span className="muted">never</span>) },
          { title: "Expires", render: (_, t) => (t.expires ? new Date(t.expires).toLocaleDateString() : "never") },
          {
            title: <span className="sr-only">Actions</span>,
            align: "right",
            render: (_, t) => (
              <Tooltip title="Revoke">
                <Button
                  danger
                  icon={<Trash2 size={14} />}
                  aria-label={`Revoke token ${t.name}`}
                  onClick={() =>
                    modal.confirm({
                      title: `Revoke “${t.name}”?`,
                      content: "Anything using it stops working at once.",
                      okText: "Revoke",
                      okButtonProps: { danger: true },
                      onOk: async () => {
                        await team.revokeAPIToken(t.id);
                        refresh();
                      },
                    })
                  }
                />
              </Tooltip>
            ),
          },
        ]}
      />
      <Button className="api-token-new" icon={<Plus size={14} />} onClick={() => setCreating(true)}>
        New token
      </Button>
      <Modal
        open={creating}
        title={made ? "Copy your token now" : "New API token"}
        onCancel={close}
        footer={
          made ? (
            <Button type="primary" onClick={close}>
              Done
            </Button>
          ) : (
            <>
              <Button onClick={close}>Cancel</Button>
              <Button
                type="primary"
                icon={<KeyRound size={14} />}
                disabled={!name.trim()}
                onClick={async () => {
                  try {
                    const r = await team.createAPIToken(name.trim(), days);
                    setMade(r.token);
                    refresh();
                  } catch (e) {
                    message.error((e as Error).message);
                  }
                }}
              >
                Create token
              </Button>
            </>
          )
        }
      >
        {made ? (
          <div className="invite-result">
            <Alert type="warning" showIcon message="This is the only time the token is shown. Store it somewhere safe, like your CI's secret settings." />
            <label className="modal-field">
              <span>Token</span>
              <CopyField label="API token" value={made} />
            </label>
          </div>
        ) : (
          <>
            <label className="modal-field">
              <span>Name</span>
              <Input autoFocus aria-label="Token name" placeholder="e.g. GitLab CI" value={name} onChange={(e) => setName(e.target.value)} />
              <span className="muted small">Where it's used, so you know what to revoke later.</span>
            </label>
            <label className="modal-field">
              <span>Expires after</span>
              <Select
                aria-label="Expires after"
                value={days}
                onChange={setDays}
                options={[
                  { value: 30, label: "30 days" },
                  { value: 90, label: "90 days" },
                  { value: 365, label: "1 year" },
                  { value: 0, label: "Never" },
                ]}
              />
            </label>
          </>
        )}
      </Modal>
    </div>
  );
}
