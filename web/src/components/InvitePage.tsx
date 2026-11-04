import { useEffect, useState } from "react";
import { Alert, Button, Result, Spin } from "antd";
import logo from "../assets/pyro-logo.png";
import { team, type AuthConfig, type InvitePreview } from "../api";
import { openProject } from "../team";
import { AuthPage } from "./AuthPage";

// /invite/<token>: shows who invited you to what, then joins after sign-in
// or sign-up.

const ROLE = { owner: "an owner", editor: "an editor", viewer: "a viewer" } as const;

/** After joining, open the workspace's first project (or the start page). */
async function goToWorkspace(wsId: string) {
  const me = await team.me();
  const ws = me.workspaces.find((w) => w.id === wsId);
  const p = ws?.projects[0];
  if (p) openProject(p.id);
  else window.location.assign("/");
}

export function InvitePage({ token, config }: { token: string; config: AuthConfig }) {
  const [preview, setPreview] = useState<InvitePreview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    team.previewInvite(token).then(setPreview, (e: Error) => setError(e.message));
  }, [token]);

  if (error) {
    return (
      <main className="auth-page">
        <Result status="warning" title="This invite can't be used" subTitle={error} extra={<Button href="/">Go to Pyro</Button>} />
      </main>
    );
  }
  if (!preview) return <Spin fullscreen />;

  const join = async () => {
    setBusy(true);
    try {
      await team.acceptInvite(token);
      await goToWorkspace(preview.workspaceId);
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  };

  const summary = (
    <p className="invite-summary">
      {preview.invitedBy ? `${preview.invitedBy} invited you` : "You're invited"} to join <strong>{preview.workspace}</strong> as {ROLE[preview.role]}.
    </p>
  );

  if (!config.signedIn) {
    return (
      <AuthPage
        config={{ ...config, signup: true, firstUser: false }}
        invite={{ token, email: preview.email, banner: summary }}
        onSignedIn={async (how) => {
          // Sign-up with an invite joins on the server; sign-in joins here.
          if (how === "signin") await team.acceptInvite(token);
          await goToWorkspace(preview.workspaceId);
        }}
      />
    );
  }

  return (
    <main className="auth-page">
      <section className="card auth-card" aria-labelledby="invite-title">
        <header className="auth-head">
          <img src={logo} alt="" width={30} height={38} />
          <div>
            <h1 id="invite-title">Join {preview.workspace}</h1>
            <p className="muted small">Pyro team server</p>
          </div>
        </header>
        {summary}
        {preview.email && <Alert type="info" showIcon className="auth-alert" message={`This invite is for ${preview.email}.`} />}
        <Button type="primary" block loading={busy} onClick={join}>
          Join workspace
        </Button>
        <p className="muted small auth-hint">
          <a href="/">Not now</a>
        </p>
      </section>
    </main>
  );
}
