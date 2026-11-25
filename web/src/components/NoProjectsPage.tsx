import { useState } from "react";
import { Alert, Button, Form, Input } from "antd";
import logo from "../assets/pyro-logo.png";
import { team, type Me } from "../api";

/**
 * Shown on a team server when the signed-in user has no project to open (for
 * example after deleting their last one). Anyone signed in may create a
 * workspace, so they can always start a project here instead of being stuck.
 */
export function NoProjectsPage({ me }: { me: Me }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const create = async ({ name }: { name: string }) => {
    setBusy(true);
    setError(null);
    try {
      // A workspace they can add projects to; otherwise a new one of their own.
      let ws = me.workspaces.find((w) => w.role === "owner" || w.role === "editor");
      ws ??= await team.createWorkspace("My workspace");
      const p = await team.createProject(ws.id, name.trim());
      window.location.assign(`/p/${encodeURIComponent(p.id)}`);
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  };

  const signOut = async () => {
    try {
      await team.logout();
    } finally {
      window.location.assign("/");
    }
  };

  return (
    <main className="auth-page">
      <section className="card auth-card" aria-labelledby="no-projects-title">
        <header className="auth-head">
          <img src={logo} alt="" width={30} height={38} />
          <div>
            <h1 id="no-projects-title">You don't have any projects</h1>
            <p className="muted small">Signed in as {me.user.email}</p>
          </div>
        </header>
        {error && <Alert type="error" showIcon className="auth-alert" message={error} />}
        <Form layout="vertical" requiredMark={false} initialValues={{ name: "My project" }} onFinish={create}>
          <Form.Item label="Project name" name="name" rules={[{ required: true, whitespace: true, message: "Enter a name" }]}>
            <Input autoFocus maxLength={100} />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={busy}>
            Create project
          </Button>
        </Form>
        <p className="muted small auth-hint">
          Or ask a workspace owner to add you to one of theirs.{" "}
          <Button type="link" onClick={signOut}>
            Sign out
          </Button>
        </p>
      </section>
    </main>
  );
}
