import { useState } from "react";
import { Alert, Button, Form, Input, Tabs } from "antd";
import logo from "../assets/pyro-logo.png";
import { team, type AuthConfig } from "../api";

// Sign in / create an account on a team server.

export function AuthPage({ config, onSignedIn }: { config: AuthConfig; onSignedIn: () => void }) {
  const [tab, setTab] = useState(config.firstUser ? "signup" : "signin");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (fn: () => Promise<unknown>) => {
    setError(null);
    setBusy(true);
    try {
      await fn();
      onSignedIn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const signIn = (
    <Form layout="vertical" requiredMark={false} onFinish={(v: { email: string; password: string }) => submit(() => team.login(v))}>
      <Form.Item label="Email" name="email" rules={[{ required: true, message: "Enter your email" }]}>
        <Input type="email" autoComplete="username" autoFocus />
      </Form.Item>
      <Form.Item label="Password" name="password" rules={[{ required: true, message: "Enter your password" }]}>
        <Input.Password autoComplete="current-password" />
      </Form.Item>
      <Button type="primary" htmlType="submit" block loading={busy}>
        Sign in
      </Button>
      <p className="muted small auth-hint">Forgot your password? Ask a Pyro administrator to reset it.</p>
    </Form>
  );

  const signUp = (
    <Form
      layout="vertical"
      requiredMark={false}
      onFinish={(v: { email: string; name: string; password: string }) => submit(() => team.signup(v))}
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
        {error && <Alert type="error" showIcon className="auth-alert" message={error} />}
        {items.length > 1 ? (
          <Tabs destroyOnHidden activeKey={tab} onChange={(k) => { setTab(k); setError(null); }} items={items} />
        ) : (
          items[0]?.children
        )}
        {!config.signup && <p className="muted small auth-hint">New here? Ask a workspace owner to invite you.</p>}
      </section>
    </main>
  );
}
