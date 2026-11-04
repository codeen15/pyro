import { useRef, useState } from "react";
import { Alert, App as AntApp, Button, Checkbox, Dropdown, Form, Input, Modal, Radio, Select, Space, Table, Tabs, Tag, type MenuProps } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { APITokens } from "./Admin";
import { ArrowLeftRight, BookOpen, Briefcase, ChevronDown, Cookie, Copy, Download, Folder, LogOut, MoreVertical, Pencil, Plus, ShieldCheck, Trash2, Upload, UserCircle, Users } from "../icons";
import { api, team, type Invite, type Member, type MemberRole, type TeamWorkspace, type TreeNode } from "../api";
import { openProject, useCanEdit, useTeam } from "../team";
import { useApp } from "../store";

// Team server UI: the project switcher and user menu in the top bar, the
// workspaces & projects page, and the account page.

const ROLE_LABEL = { owner: "Owner", editor: "Editor", viewer: "Viewer" } as const;

function useWorkspaces(enabled = true) {
  return useQuery({ queryKey: ["team-workspaces"], queryFn: team.workspaces, enabled });
}

export function ProjectSwitcher() {
  const project = useTeam((s) => s.project);
  const select = useApp((s) => s.select);
  const ws = useWorkspaces();
  if (!project) return null;

  const items: MenuProps["items"] = [
    ...(ws.data ?? []).map((w) => ({
      type: "group" as const,
      key: w.id,
      label: w.name,
      children: w.projects.length
        ? w.projects.map((p) => ({ key: p.id, label: p.name, icon: <Folder size={14} />, disabled: p.id === project.id }))
        : [{ key: `empty-${w.id}`, label: "No projects yet", disabled: true }],
    })),
    { type: "divider" as const },
    { key: "manage", label: "Workspaces and projects", icon: <Briefcase size={14} /> },
  ];

  return (
    <Dropdown
      trigger={["click"]}
      menu={{
        items,
        onClick: ({ key }) => (key === "manage" ? select("projects") : openProject(key)),
      }}
    >
      <button className="ws-name ws-switch" aria-label={`Project: ${project.name}. Switch project`}>
        <Briefcase size={16} />
        <span className="ws-path">
          <span className="muted">{project.workspaceName}</span>
          <span aria-hidden> / </span>
          {project.name}
        </span>
        <ChevronDown size={12} />
      </button>
    </Dropdown>
  );
}

export function UserMenu() {
  const me = useTeam((s) => s.me);
  const select = useApp((s) => s.select);
  if (!me) return null;
  return (
    <Dropdown
      trigger={["click"]}
      menu={{
        items: [
          { key: "who", type: "group", label: `${me.user.name} · ${me.user.email}` },
          { key: "account", label: "Account", icon: <UserCircle size={14} /> },
          { key: "projects", label: "Workspaces and projects", icon: <Briefcase size={14} /> },
          ...(me.user.admin ? [{ key: "admin", label: "Administration", icon: <ShieldCheck size={14} /> }] : []),
          // The top bar hides these buttons on phones; keep them reachable here.
          { key: "cookies", label: "Cookies", icon: <Cookie size={14} />, className: "menu-phone-only" },
          { key: "docs", label: "User guide", icon: <BookOpen size={14} />, className: "menu-phone-only" },
          { type: "divider" },
          { key: "signout", label: "Sign out", icon: <LogOut size={14} /> },
        ],
        onClick: async ({ key }) => {
          if (key === "signout") {
            await team.logout().catch(() => undefined);
            window.location.assign("/");
          } else select(key);
        },
      }}
    >
      <Button type="text" icon={<UserCircle size={18} />} aria-label={`Account menu for ${me.user.name}`} />
    </Dropdown>
  );
}

/** Asks for a name in a modal; resolves when saved. */
function useNamePrompt() {
  const { modal } = AntApp.useApp();
  return (title: string, okText: string, initial: string, save: (name: string) => Promise<unknown>) => {
    let value = initial;
    modal.confirm({
      title,
      icon: null,
      okText,
      content: <Input autoFocus defaultValue={initial} aria-label="Name" onChange={(e) => (value = e.target.value)} />,
      onOk: () => save(value.trim()),
    });
  };
}

export function ProjectsPage() {
  const qc = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const ws = useWorkspaces();
  const current = useTeam((s) => s.project);
  const prompt = useNamePrompt();
  const refresh = () => qc.invalidateQueries({ queryKey: ["team-workspaces"] });

  const guard = async (fn: () => Promise<unknown>, done?: string) => {
    try {
      await fn();
      if (done) message.success(done);
      refresh();
    } catch (e) {
      message.error((e as Error).message);
      throw e;
    }
  };

  return (
    <div className="page">
      <section className="card projects-page">
        <header className="page-head">
          <div>
            <h1>Workspaces and projects</h1>
            <p className="muted small">
              Your personal workspace is yours alone. Team workspaces are shared with their members. Each project has its own collections, environments, load tests
              and test data.
            </p>
          </div>
          <div className="page-actions">
            <Button
              icon={<Plus size={14} />}
              onClick={() => prompt("New team workspace", "Create", "", (name) => guard(() => team.createWorkspace(name), "Workspace created"))}
            >
              New workspace
            </Button>
          </div>
        </header>
        <div className="ws-list">
          {(ws.data ?? []).map((w) => (
            <WorkspaceCard
              key={w.id}
              ws={w}
              all={ws.data ?? []}
              refresh={refresh}
              current={current?.id}
              onNewProject={() =>
                prompt(`New project in ${w.name}`, "Create", "", (name) =>
                  guard(async () => {
                    const p = await team.createProject(w.id, name);
                    openProject(p.id);
                  }),
                )
              }
              onRenameWorkspace={() => prompt("Rename workspace", "Rename", w.name, (name) => guard(() => team.renameWorkspace(w.id, name)))}
              onDeleteWorkspace={() =>
                modal.confirm({
                  title: `Delete the workspace “${w.name}”?`,
                  content: "Only empty workspaces can be deleted.",
                  okText: "Delete",
                  okButtonProps: { danger: true },
                  onOk: () => guard(() => team.deleteWorkspace(w.id), "Workspace deleted"),
                })
              }
              onRenameProject={(id, name) => prompt("Rename project", "Rename", name, (n) => guard(() => team.renameProject(id, n)))}
              onDeleteProject={(id, name) =>
                modal.confirm({
                  title: `Delete the project “${name}”?`,
                  content: "Its collections, environments, load tests and data files are deleted for every member. This can't be undone.",
                  okText: "Delete project",
                  okButtonProps: { danger: true },
                  onOk: () =>
                    guard(async () => {
                      await team.deleteProject(id);
                      if (id === current?.id) window.location.assign("/");
                    }, "Project deleted"),
                })
              }
            />
          ))}
        </div>
      </section>
    </div>
  );
}

function WorkspaceCard({
  ws,
  all,
  current,
  onNewProject,
  onRenameWorkspace,
  onDeleteWorkspace,
  onRenameProject,
  onDeleteProject,
  refresh,
}: {
  ws: TeamWorkspace;
  all: TeamWorkspace[];
  current?: string;
  onNewProject: () => void;
  onRenameWorkspace: () => void;
  onDeleteWorkspace: () => void;
  onRenameProject: (id: string, name: string) => void;
  onDeleteProject: (id: string, name: string) => void;
  refresh: () => void;
}) {
  const { message, modal } = AntApp.useApp();
  const select = useApp((s) => s.select);
  const upload = useRef<HTMLInputElement>(null);
  const owner = ws.role === "owner";
  const canCreate = ws.role !== "viewer";
  // Projects move between workspaces the user owns.
  const targets = all.filter((w) => w.id !== ws.id && w.role === "owner");

  const download = async (id: string, name: string) => {
    try {
      const blob = await team.archive(id);
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `${name}.zip`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const move = (id: string, name: string) => {
    let to = targets[0]?.id;
    modal.confirm({
      title: `Move “${name}” to another workspace`,
      icon: null,
      okText: "Move project",
      content: (
        <label className="modal-field">
          <span>Workspace</span>
          <Select
            aria-label="Target workspace"
            defaultValue={to}
            onChange={(v) => (to = v)}
            options={targets.map((w) => ({ value: w.id, label: w.kind === "personal" ? `${w.name} (only you)` : w.name }))}
          />
          <span className="muted small">Its members change to the target workspace's members.</span>
        </label>
      ),
      onOk: async () => {
        try {
          await team.moveProject(id, to!);
          message.success("Project moved");
          refresh();
        } catch (e) {
          message.error((e as Error).message);
          throw e;
        }
      },
    });
  };

  const uploadZip = async (file: File) => {
    try {
      const p = await team.importProject(ws.id, file.name.replace(/\.zip$/i, ""), file);
      openProject(p.id);
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  return (
    <section className="ws-card" aria-labelledby={`ws-${ws.id}`}>
      <header className="ws-card-head">
        <span className="ws-card-title">
          {ws.kind === "team" ? <Users size={16} /> : <Briefcase size={16} />}
          <h2 id={`ws-${ws.id}`}>{ws.name}</h2>
          <Tag>{ws.kind === "personal" ? "Only you" : ROLE_LABEL[ws.role]}</Tag>
        </span>
        <span className="ws-card-actions">
          {ws.kind === "team" && (
            <Button icon={<Users size={14} />} onClick={() => select(`workspace:${ws.id}`)}>
              Members
            </Button>
          )}
          {canCreate && (
            <Button icon={<Plus size={14} />} onClick={onNewProject}>
              New project
            </Button>
          )}
          <Dropdown
            trigger={["click"]}
            menu={{
              items: [
                ...(canCreate ? [{ key: "upload", label: "Upload a project (.zip)", icon: <Upload size={14} /> }] : []),
                ...(owner && ws.kind === "team"
                  ? [
                      { key: "rename", label: "Rename workspace", icon: <Pencil size={14} /> },
                      { key: "delete", label: "Delete workspace", icon: <Trash2 size={14} />, danger: true },
                    ]
                  : []),
              ],
              onClick: ({ key }) => (key === "upload" ? upload.current?.click() : key === "rename" ? onRenameWorkspace() : onDeleteWorkspace()),
            }}
          >
            <Button icon={<MoreVertical size={14} />} aria-label={`More actions for workspace ${ws.name}`} disabled={!canCreate && !owner} />
          </Dropdown>
          <input
            ref={upload}
            type="file"
            accept=".zip,application/zip"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (f) uploadZip(f);
            }}
          />
        </span>
      </header>
      {ws.projects.length === 0 ? (
        <p className="muted small ws-empty">No projects yet.</p>
      ) : (
        <ul className="project-list">
          {ws.projects.map((p) => (
            <li key={p.id} className={p.id === current ? "is-current" : undefined}>
              <button className="project-open" onClick={() => openProject(p.id)} aria-current={p.id === current ? "page" : undefined}>
                <Folder size={15} />
                <span>{p.name}</span>
                {p.id === current && <span className="muted small">open</span>}
              </button>
              <Dropdown
                trigger={["click"]}
                menu={{
                  items: [
                    { key: "download", label: "Download as .zip", icon: <Download size={14} /> },
                    ...(owner
                      ? [
                          { key: "rename", label: "Rename", icon: <Pencil size={14} /> },
                          ...(targets.length ? [{ key: "move", label: "Move to workspace…", icon: <ArrowLeftRight size={14} /> }] : []),
                          { type: "divider" as const },
                          { key: "delete", label: "Delete project", icon: <Trash2 size={14} />, danger: true },
                        ]
                      : []),
                  ],
                  onClick: ({ key }) => {
                    if (key === "download") download(p.id, p.name);
                    else if (key === "rename") onRenameProject(p.id, p.name);
                    else if (key === "move") move(p.id, p.name);
                    else if (key === "delete") onDeleteProject(p.id, p.name);
                  },
                }}
              >
                <Button type="text" icon={<MoreVertical size={14} />} aria-label={`More actions for project ${p.name}`} />
              </Dropdown>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

const ROLE_OPTIONS = [
  { value: "viewer", label: "Viewer" },
  { value: "editor", label: "Editor" },
  { value: "owner", label: "Owner" },
];

const ROLE_HELP: Record<MemberRole, string> = {
  viewer: "Opens everything, sends requests and runs collections; can't change anything.",
  editor: "Also creates and changes collections, environments, load tests and data, and starts load tests.",
  owner: "Also manages members and invites, and renames, moves and deletes projects.",
};

/** A team workspace's members, invites and activity. */
export function WorkspacePage({ id }: { id: string }) {
  const qc = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const me = useTeam((s) => s.me);
  const select = useApp((s) => s.select);
  const ws = useWorkspaces();
  const info = ws.data?.find((w) => w.id === id);
  const owner = info?.role === "owner";
  const members = useQuery({ queryKey: ["team-members", id], queryFn: () => team.members(id), enabled: !!info });
  const invites = useQuery({ queryKey: ["team-invites", id], queryFn: () => team.invites(id), enabled: owner });
  const activity = useQuery({ queryKey: ["team-activity", id], queryFn: () => team.activity(id), enabled: !!info });
  const [inviting, setInviting] = useState(false);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["team-members", id] });
    qc.invalidateQueries({ queryKey: ["team-invites", id] });
    qc.invalidateQueries({ queryKey: ["team-activity", id] });
  };
  const guard = async (fn: () => Promise<unknown>, done?: string) => {
    try {
      await fn();
      if (done) message.success(done);
      refresh();
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  if (ws.isPending) return <div className="page" />;
  if (!info) {
    return (
      <div className="page">
        <section className="card projects-page">
          <p>This workspace isn't available.</p>
          <Button onClick={() => select("projects")}>Back to workspaces</Button>
        </section>
      </div>
    );
  }

  const memberRows = (
    <Table<Member>
      rowKey="userId"
      size="small"
      pagination={false}
      scroll={{ x: "max-content" }}
      loading={members.isPending}
      dataSource={members.data ?? []}
      columns={[
        {
          title: "Name",
          render: (_, m) => (
            <span className="member-name">
              <strong>{m.name}</strong>
              {m.userId === me?.user.id && <span className="muted small"> (you)</span>}
              <span className="muted small member-email">{m.email}</span>
            </span>
          ),
        },
        {
          title: "Role",
          width: 150,
          render: (_, m) =>
            owner ? (
              <Select
                aria-label={`Role of ${m.name}`}
                value={m.role}
                options={ROLE_OPTIONS}
                style={{ width: 120 }}
                onChange={(role) => guard(() => team.setRole(id, m.userId, role as MemberRole), "Role changed")}
              />
            ) : (
              ROLE_LABEL[m.role]
            ),
        },
        {
          title: <span className="sr-only">Actions</span>,
          width: 110,
          align: "right",
          render: (_, m) => {
            const self = m.userId === me?.user.id;
            if (!owner && !self) return null;
            return (
              <Button
                danger
                onClick={() =>
                  modal.confirm({
                    title: self ? `Leave ${info.name}?` : `Remove ${m.name} from ${info.name}?`,
                    content: self ? "You'll lose access to its projects until someone invites you again." : "They lose access to its projects.",
                    okText: self ? "Leave" : "Remove",
                    okButtonProps: { danger: true },
                    onOk: () =>
                      guard(async () => {
                        await team.removeMember(id, m.userId);
                        if (self) window.location.assign("/");
                      }),
                  })
                }
              >
                {self ? "Leave" : "Remove"}
              </Button>
            );
          },
        },
      ]}
    />
  );

  return (
    <div className="page">
      <section className="card projects-page">
        <header className="page-head">
          <div>
            <h1>{info.name}</h1>
            <p className="muted small">
              Team workspace · you are {ROLE_LABEL[info.role].toLowerCase()} · {info.projects.length} project{info.projects.length === 1 ? "" : "s"}
            </p>
          </div>
          <div className="page-actions">
            <Button onClick={() => select("projects")}>All workspaces</Button>
            {owner && (
              <Button type="primary" icon={<Plus size={14} />} onClick={() => setInviting(true)}>
                Invite people
              </Button>
            )}
          </div>
        </header>
        <Tabs
          items={[
            { key: "members", label: `Members${members.data ? ` (${members.data.length})` : ""}`, children: memberRows },
            ...(owner
              ? [
                  {
                    key: "invites",
                    label: `Open invites${invites.data?.length ? ` (${invites.data.length})` : ""}`,
                    children: (
                      <Table<Invite>
                        rowKey="id"
                        size="small"
                        pagination={false}
                        scroll={{ x: "max-content" }}
                        loading={invites.isPending}
                        locale={{ emptyText: "No open invites." }}
                        dataSource={invites.data ?? []}
                        columns={[
                          { title: "For", render: (_, i) => i.email || <span className="muted">Anyone with the link</span> },
                          { title: "Role", width: 100, render: (_, i) => ROLE_LABEL[i.role] },
                          { title: "Used", width: 80, render: (_, i) => (i.maxUses ? `${i.uses} / ${i.maxUses}` : String(i.uses)) },
                          { title: "Expires", width: 130, render: (_, i) => new Date(i.expires).toLocaleDateString() },
                          {
                            title: <span className="sr-only">Actions</span>,
                            width: 100,
                            align: "right",
                            render: (_, i) => (
                              <Button danger onClick={() => guard(() => team.revokeInvite(id, i.id), "Invite revoked")}>
                                Revoke
                              </Button>
                            ),
                          },
                        ]}
                      />
                    ),
                  },
                ]
              : []),
            {
              key: "activity",
              label: "Activity",
              children: (
                <ul className="activity-list">
                  {(activity.data ?? []).length === 0 && <li className="muted small">Nothing yet.</li>}
                  {(activity.data ?? []).map((a, i) => (
                    <li key={i}>
                      <span>
                        <strong>{a.user}</strong> {a.action}
                        {a.target && <> <code>{a.target}</code></>}
                        {a.project && <span className="muted"> in {a.project}</span>}
                      </span>
                      <time className="muted small" dateTime={a.at}>
                        {new Date(a.at).toLocaleString()}
                      </time>
                    </li>
                  ))}
                </ul>
              ),
            },
          ]}
        />
      </section>
      <InviteModal open={inviting} wsId={id} wsName={info.name} onClose={() => { setInviting(false); refresh(); }} />
    </div>
  );
}

function InviteModal({ open, wsId, wsName, onClose }: { open: boolean; wsId: string; wsName: string; onClose: () => void }) {
  const { message } = AntApp.useApp();
  const [role, setRole] = useState<MemberRole>("editor");
  const [email, setEmail] = useState("");
  const [days, setDays] = useState(7);
  const [result, setResult] = useState<{ url: string; emailed: boolean; emailError?: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const canEmail = useTeamEmail();
  const close = () => {
    setResult(null);
    setEmail("");
    onClose();
  };
  const create = async () => {
    setBusy(true);
    try {
      setResult(await team.createInvite(wsId, { role, email: email.trim() || undefined, days }));
    } catch (e) {
      message.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open={open}
      title={`Invite people to ${wsName}`}
      onCancel={close}
      footer={
        result ? (
          <Button type="primary" onClick={close}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={close}>Cancel</Button>
            <Button type="primary" loading={busy} onClick={create}>
              {email.trim() ? "Create invite" : "Create invite link"}
            </Button>
          </>
        )
      }
    >
      {result ? (
        <div className="invite-result">
          {result.emailed && <Alert type="success" showIcon message={`Invite emailed to ${email.trim()}.`} />}
          {result.emailError && <Alert type="warning" showIcon message={result.emailError} />}
          <label className="modal-field">
            <span>Invite link</span>
            <Space.Compact block>
              <Input readOnly value={result.url} aria-label="Invite link" onFocus={(e) => e.target.select()} />
              <Button
                icon={<Copy size={14} />}
                onClick={() => navigator.clipboard.writeText(result.url).then(() => message.success("Link copied"), () => message.error("Couldn't copy; select the link and copy it"))}
              >
                Copy
              </Button>
            </Space.Compact>
            <span className="muted small">
              {email.trim() ? `Only ${email.trim()} can use it, once.` : "Anyone with the link can join until it expires."} It expires in {days} day{days === 1 ? "" : "s"}.
            </span>
          </label>
        </div>
      ) : (
        <div className="invite-form">
          <label className="modal-field">
            <span>Role</span>
            <Select aria-label="Role" value={role} onChange={setRole} options={ROLE_OPTIONS} />
            <span className="muted small">{ROLE_HELP[role]}</span>
          </label>
          <label className="modal-field">
            <span>Email (optional)</span>
            <Input type="email" aria-label="Email (optional)" placeholder="name@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
            <span className="muted small">
              {canEmail ? "We'll email the link, and only this address can use it." : "Only this address can use the link. Leave empty for a link anyone you send it to can use."}
            </span>
          </label>
          <label className="modal-field">
            <span>Expires after</span>
            <Select
              aria-label="Expires after"
              value={days}
              onChange={setDays}
              options={[1, 7, 14, 30].map((d) => ({ value: d, label: `${d} day${d === 1 ? "" : "s"}` }))}
            />
          </label>
        </div>
      )}
    </Modal>
  );
}

/** Whether the server can email invites. */
function useTeamEmail() {
  const q = useQuery({ queryKey: ["auth-config"], queryFn: team.config, staleTime: Infinity });
  return !!q.data?.email;
}

export function AccountPage() {
  const me = useTeam((s) => s.me);
  const setTeam = useTeam((s) => s.set);
  const { message, modal } = AntApp.useApp();
  const [pwForm] = Form.useForm();
  const [busy, setBusy] = useState(false);
  if (!me) return null;
  const local = me.user.provider === "local";

  return (
    <div className="page">
      <section className="card account-page">
        <header className="page-head">
          <div>
            <h1>Account</h1>
            <p className="muted small">
              {me.user.email}
              {me.user.admin && " · server administrator"}
            </p>
          </div>
        </header>
        <Form
          layout="vertical"
          requiredMark={false}
          initialValues={{ name: me.user.name }}
          onFinish={async (v: { name: string }) => {
            try {
              setTeam({ me: await team.rename(v.name) });
              message.success("Name saved");
            } catch (e) {
              message.error((e as Error).message);
            }
          }}
          className="account-form"
        >
          <h2>Profile</h2>
          <Form.Item label="Name" name="name" rules={[{ required: true, message: "Enter your name" }]}>
            <Input autoComplete="name" />
          </Form.Item>
          <Button htmlType="submit">Save name</Button>
        </Form>
        {local ? (
          <Form
            form={pwForm}
            layout="vertical"
            requiredMark={false}
            className="account-form"
            onFinish={async (v: { current: string; password: string }) => {
              setBusy(true);
              try {
                await team.changePassword(v.current, v.password);
                pwForm.resetFields();
                message.success("Password changed. Your other sessions were signed out.");
              } catch (e) {
                message.error((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <h2>Password</h2>
            <Form.Item label="Current password" name="current" rules={[{ required: true, message: "Enter your current password" }]}>
              <Input.Password autoComplete="current-password" />
            </Form.Item>
            <Form.Item label="New password" name="password" extra="At least 10 characters." rules={[{ required: true, min: 10, message: "Use at least 10 characters" }]}>
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            <Button htmlType="submit" loading={busy}>
              Change password
            </Button>
          </Form>
        ) : (
          <p className="muted small">You sign in through {me.user.provider.toUpperCase()}; change your password there.</p>
        )}
        <APITokens />
        <div className="account-form">
          <h2>Sessions</h2>
          <p className="muted small">Signs you out on every browser and app, including this one. Personal API tokens keep working; revoke those above.</p>
          <Button
            danger
            onClick={() =>
              modal.confirm({
                title: "Sign out everywhere?",
                content: "Every browser and app signed in as you has to sign in again.",
                okText: "Sign out everywhere",
                okButtonProps: { danger: true },
                onOk: async () => {
                  await team.logoutEverywhere();
                  window.location.assign("/");
                },
              })
            }
          >
            Sign out everywhere
          </Button>
        </div>
      </section>
    </div>
  );
}

/** Copies or moves a collection to another project (or copies it within this one). */
export function TransferModal({ collection, onClose }: { collection: TreeNode | null; onClose: () => void }) {
  const qc = useQueryClient();
  const { message } = AntApp.useApp();
  const project = useTeam((s) => s.project);
  const canEdit = useCanEdit();
  const ws = useWorkspaces(!!collection);
  const [target, setTarget] = useState<string | undefined>();
  const [mode, setMode] = useState<"copy" | "move">("copy");
  const [envs, setEnvs] = useState(true);
  const [busy, setBusy] = useState(false);
  const usage = useQuery({
    queryKey: ["collection-usage", collection?.path],
    queryFn: () => api.collectionUsage(collection!.path),
    enabled: !!collection && mode === "move",
  });

  // Projects you can add to: workspaces where you edit.
  const writable = (ws.data ?? []).filter((w) => w.role !== "viewer");
  const options = writable.map((w) => ({
    label: w.name,
    options: w.projects.map((p) => ({ value: p.id, label: p.id === project?.id ? `${p.name} (this project)` : p.name })),
  }));
  const sameProject = target === project?.id;
  const targetName = writable.flatMap((w) => w.projects).find((p) => p.id === target)?.name;

  const close = () => {
    setTarget(undefined);
    setMode("copy");
    onClose();
  };
  const run = async () => {
    if (!collection || !project || !target) return;
    setBusy(true);
    try {
      const res = await team.transfer(project.id, { collection: collection.path, project: target, mode, environments: envs && !sameProject });
      await qc.invalidateQueries({ queryKey: ["workspace"] });
      message.success(`${mode === "copy" ? "Copied" : "Moved"} ${collection.name} to ${targetName}${res.slug !== collection.path ? ` as “${res.slug}”` : ""}`);
      close();
    } catch (e) {
      message.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={!!collection}
      title={`${canEdit ? "Move or copy" : "Copy"} “${collection?.name ?? ""}”`}
      onCancel={close}
      okText={mode === "copy" ? "Copy collection" : "Move collection"}
      okButtonProps={{ disabled: !target || (mode === "move" && sameProject), loading: busy }}
      onOk={run}
    >
      <label className="modal-field">
        <span>To project</span>
        <Select aria-label="Target project" placeholder="Pick a project" value={target} onChange={setTarget} options={options} showSearch optionFilterProp="label" />
        {writable.length === 0 && <span className="muted small">You aren't an editor in any workspace.</span>}
      </label>
      {canEdit && (
        <div className="modal-field">
          <span>What to do</span>
          <Radio.Group
            value={mode}
            onChange={(e) => setMode(e.target.value)}
            options={[
              { value: "copy", label: "Copy (keep it here too)" },
              { value: "move", label: "Move (remove it from here)", disabled: sameProject },
            ]}
          />
        </div>
      )}
      {!sameProject && (
        <Checkbox checked={envs} onChange={(e) => setEnvs(e.target.checked)}>
          Also copy environments the other project doesn't have
        </Checkbox>
      )}
      {mode === "move" && (usage.data?.loadTests.length ?? 0) > 0 && (
        <Alert
          className="transfer-warning"
          type="warning"
          showIcon
          message={`Load tests here use this collection: ${usage.data!.loadTests.join(", ")}. They'll stop working after the move.`}
        />
      )}
    </Modal>
  );
}
