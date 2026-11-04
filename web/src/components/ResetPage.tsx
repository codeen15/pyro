import { useEffect, useState } from "react";
import { Alert, Button, Form, Input, Result, Spin } from "antd";
import logo from "../assets/pyro-logo.png";
import { team } from "../api";

// /reset/<token>: choose a new password from a reset link, then sign in.

export function ResetPage({ token }: { token: string }) {
  const [email, setEmail] = useState<string | null>(null);
  const [gone, setGone] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    team.previewReset(token).then((r) => setEmail(r.email), (e: Error) => setGone(e.message));
  }, [token]);

  if (gone) {
    return (
      <main className="auth-page">
        <Result status="warning" title="This link can't be used" subTitle={gone} extra={<Button href="/">Go to sign in</Button>} />
      </main>
    );
  }
  if (!email) return <Spin fullscreen />;

  return (
    <main className="auth-page">
      <section className="card auth-card" aria-labelledby="reset-title">
        <header className="auth-head">
          <img src={logo} alt="" width={30} height={38} />
          <div>
            <h1 id="reset-title">Choose a new password</h1>
            <p className="muted small">{email}</p>
          </div>
        </header>
        {error && <Alert type="error" showIcon className="auth-alert" message={error} />}
        <Form
          layout="vertical"
          requiredMark={false}
          onFinish={async (v: { password: string }) => {
            setError(null);
            setBusy(true);
            try {
              await team.resetPassword(token, v.password);
              window.location.assign("/");
            } catch (e) {
              setError((e as Error).message);
              setBusy(false);
            }
          }}
        >
          <Form.Item label="New password" name="password" extra="At least 10 characters. You'll be signed out everywhere else." rules={[{ required: true, min: 10, message: "Use at least 10 characters" }]}>
            <Input.Password autoComplete="new-password" autoFocus />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={busy}>
            Set password and sign in
          </Button>
        </Form>
      </section>
    </main>
  );
}
