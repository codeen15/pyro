import { useState } from "react";
import { App as AntApp, Button, Dropdown, Form, Input, Tag, Tooltip, type MenuProps } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { BookOpen, Briefcase, ChevronDown, Cookie, Folder, LogOut, Pencil, Plus, Trash2, UserCircle, Users } from "../icons";
import { team, type TeamWorkspace } from "../api";
import { openProject, useTeam } from "../team";
import { useApp } from "../store";

// Team server UI: the project switcher and user menu in the top bar, the
// workspaces & projects page, and the account page.

const ROLE_LABEL = { owner: "Owner", editor: "Editor", viewer: "Viewer" } as const;

function useWorkspaces() {
  return useQuery({ queryKey: ["team-workspaces"], queryFn: team.workspaces });
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
  current,
  onNewProject,
  onRenameWorkspace,
  onDeleteWorkspace,
  onRenameProject,
  onDeleteProject,
}: {
  ws: TeamWorkspace;
  current?: string;
  onNewProject: () => void;
  onRenameWorkspace: () => void;
  onDeleteWorkspace: () => void;
  onRenameProject: (id: string, name: string) => void;
  onDeleteProject: (id: string, name: string) => void;
}) {
  const owner = ws.role === "owner";
  const canCreate = ws.role !== "viewer";
  return (
    <section className="ws-card" aria-labelledby={`ws-${ws.id}`}>
      <header className="ws-card-head">
        <span className="ws-card-title">
          {ws.kind === "team" ? <Users size={16} /> : <Briefcase size={16} />}
          <h2 id={`ws-${ws.id}`}>{ws.name}</h2>
          <Tag>{ws.kind === "personal" ? "Only you" : ROLE_LABEL[ws.role]}</Tag>
        </span>
        <span className="ws-card-actions">
          {canCreate && (
            <Button icon={<Plus size={14} />} onClick={onNewProject}>
              New project
            </Button>
          )}
          {owner && ws.kind === "team" && (
            <>
              <Tooltip title="Rename workspace">
                <Button icon={<Pencil size={14} />} onClick={onRenameWorkspace} aria-label={`Rename workspace ${ws.name}`} />
              </Tooltip>
              <Tooltip title="Delete workspace">
                <Button danger icon={<Trash2 size={14} />} onClick={onDeleteWorkspace} aria-label={`Delete workspace ${ws.name}`} />
              </Tooltip>
            </>
          )}
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
              {owner && (
                <span className="project-actions">
                  <Tooltip title="Rename project">
                    <Button type="text" icon={<Pencil size={14} />} onClick={() => onRenameProject(p.id, p.name)} aria-label={`Rename project ${p.name}`} />
                  </Tooltip>
                  <Tooltip title="Delete project">
                    <Button type="text" danger icon={<Trash2 size={14} />} onClick={() => onDeleteProject(p.id, p.name)} aria-label={`Delete project ${p.name}`} />
                  </Tooltip>
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export function AccountPage() {
  const me = useTeam((s) => s.me);
  const setTeam = useTeam((s) => s.set);
  const { message } = AntApp.useApp();
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
      </section>
    </div>
  );
}
