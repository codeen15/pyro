import { useState, type ReactNode } from "react";
import { Alert, Button, Divider, Form, Input, Tabs } from "antd";
import { KeyRound } from "../icons";
import logo from "../assets/pyro-logo.png";
import { team, type AuthConfig } from "../api";

// Sign in / create an account on a team server.

export interface InviteContext {
  token: string;
  email?: string;
  banner: ReactNode;
}

export function AuthPage({
  config,
  onSignedIn,
  invite,
}: {
  config: AuthConfig;
  onSignedIn: (how: "signin" | "signup") => void | Promise<void>;
  invite?: InviteContext;
}) {
  const [tab, setTab] = useState(config.firstUser || invite ? "signup" : "signin");
  // A failed single sign-on comes back as ?signin_error=…; show it once.
  const [error, setError] = useState<string | null>(() => {
    const params = new URLSearchParams(window.location.search);
    const e = params.get("signin_error");
    if (e) {
      params.delete("signin_error");
      const qs = params.toString();
      window.history.replaceState(null, "", window.location.pathname + (qs ? `?${qs}` : ""));
    }
    return e;
  });
  const [busy, setBusy] = useState(false);
  const [forgot, setForgot] = useState<"closed" | "open" | "sent">("closed");
  const providers = config.providers ?? [];

  const submit = async (how: "signin" | "signup", fn: () => Promise<unknown>) => {
    setError(null);
    setBusy(true);
    try {
      await fn();
      await onSignedIn(how);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const signIn = (
    <Form layout="vertical" requiredMark={false} initialValues={{ email: invite?.email }} onFinish={(v: { email: string; password: string }) => submit("signin", () => team.login(v))}>
      <Form.Item
        label={config.ldap ? "Email or username" : "Email"}
        name="email"
        rules={[{ required: true, message: config.ldap ? "Enter your email or username" : "Enter your email" }]}
      >
        <Input type={config.ldap ? "text" : "email"} autoComplete="username" autoFocus={!providers.length} />
      </Form.Item>
      <Form.Item label="Password" name="password" rules={[{ required: true, message: "Enter your password" }]}>
        <Input.Password autoComplete="current-password" />
      </Form.Item>
      <Button type="primary" htmlType="submit" block loading={busy}>
        Sign in
      </Button>
      {config.email ? (
        <p className="auth-hint">
          <Button type="link" onClick={() => setForgot("open")}>
            Forgot password?
          </Button>
        </p>
      ) : (
        <p className="muted small auth-hint">Forgot your password? Ask a Pyro administrator to reset it.</p>
      )}
    </Form>
  );

  const forgotForm =
    forgot === "sent" ? (
      <>
        <Alert type="success" showIcon className="auth-alert" message="If that address has a Pyro account, we've emailed it a link. It works for an hour." />
        <Button block onClick={() => setForgot("closed")}>
          Back to sign in
        </Button>
      </>
    ) : (
      <Form
        layout="vertical"
        requiredMark={false}
        onFinish={async (v: { email: string }) => {
          setError(null);
          setBusy(true);
          try {
            await team.forgotPassword(v.email);
            setForgot("sent");
          } catch (e) {
            setError((e as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <p className="muted small">We'll email you a link to choose a new password.</p>
        <Form.Item label="Email" name="email" rules={[{ required: true, message: "Enter your email" }]}>
          <Input type="email" autoComplete="username" autoFocus />
        </Form.Item>
        <Button type="primary" htmlType="submit" block loading={busy}>
          Email me a link
        </Button>
        <p className="auth-hint">
          <Button type="link" onClick={() => setForgot("closed")}>
            Back to sign in
          </Button>
        </p>
      </Form>
    );

  const sso = providers.length > 0 && (
    <div className="sso-list">
      {providers.map((p) => (
        <Button key={p.id} block icon={<KeyRound size={14} />} href={`/auth/oidc/${encodeURIComponent(p.id)}/start${invite ? `?invite=${encodeURIComponent(invite.token)}` : ""}`}>
          Sign in with {p.name}
        </Button>
      ))}
      <Divider plain className="sso-divider">
        or
      </Divider>
    </div>
  );

  const signUp = (
    <Form
      layout="vertical"
      requiredMark={false}
      initialValues={{ email: invite?.email }}
      onFinish={(v: { email: string; name: string; password: string }) => submit("signup", () => team.signup({ ...v, invite: invite?.token }))}
    >
      {config.firstUser && (
        <Alert type="info" showIcon className="auth-alert" message="No accounts yet. The account you create becomes the server administrator." />
      )}
      {config.signupPolicy === "domains" && !config.firstUser && (
        <p className="muted small">Use your {config.signupDomains?.join(" or ")} email address.</p>
      )}
      <Form.Item label="Name" name="name">
        <Input autoComplete="name" autoFocus />
      </Form.Item>
      <Form.Item label="Email" name="email" rules={[{ required: true, message: "Enter your email" }]}>
        <Input type="email" autoComplete="username" />
      </Form.Item>
      <Form.Item label="Password" name="password" extra="At least 10 characters." rules={[{ required: true, min: 10, message: "Use at least 10 characters" }]}>
        <Input.Password autoComplete="new-password" />
      </Form.Item>
      <Button type="primary" htmlType="submit" block loading={busy}>
        Create account
      </Button>
    </Form>
  );

  const items = [
    ...(config.firstUser ? [] : [{ key: "signin", label: "Sign in", children: signIn }]),
    ...(config.signup ? [{ key: "signup", label: "Create account", children: signUp }] : []),
  ];

  return (
    <main className="auth-page">
      <section className="card auth-card" aria-labelledby="auth-title">
        <header className="auth-head">
          <img src={logo} alt="" width={30} height={38} />
          <div>
            <h1 id="auth-title">Pyro</h1>
            <p className="muted small">Team server</p>
          </div>
        </header>
        {invite?.banner}
        {error && <Alert type="error" showIcon className="auth-alert" message={error} />}
        {forgot !== "closed" ? (
          forgotForm
        ) : (
          <>
            {sso}
            {items.length > 1 ? (
              <Tabs destroyOnHidden activeKey={tab} onChange={(k) => { setTab(k); setError(null); }} items={items} />
            ) : (
              items[0]?.children
            )}
          </>
        )}
        {!config.signup && !invite && <p className="muted small auth-hint">New here? Ask a workspace owner to invite you.</p>}
      </section>
    </main>
  );
}
