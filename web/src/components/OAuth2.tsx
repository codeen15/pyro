import { useEffect, useId, useState } from "react";
import { Alert, Button, Collapse, Input, Select, Tooltip } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, KeyRound, LogIn, RefreshCw, Trash2 } from "../icons";
import { api, type PyroRequest } from "../api";
import { useApp } from "../store";

// OAuth 2.0 settings (Postman's parameter names) and the token panel.

export const GRANTS: { id: string; label: string }[] = [
  { id: "authorization_code_with_pkce", label: "Authorization code (with PKCE)" },
  { id: "authorization_code", label: "Authorization code" },
  { id: "client_credentials", label: "Client credentials" },
  { id: "password_credentials", label: "Password credentials" },
  { id: "", label: "Paste an access token" },
];

type Params = Record<string, string>;
type Field = { key: string; label: string; secret?: boolean; placeholder?: string };

export function OAuth2Form({ params, onChange, context }: { params: Params; onChange: (p: Params) => void; context?: TokenContext }) {
  // Settings imported with a token but no grant show as "paste a token".
  const grant = params.grant_type ?? (params.accessTokenUrl || params.authUrl ? "authorization_code" : "");
  const set = (k: string, v: string) => {
    const next = { ...params, [k]: v };
    if (v === "") delete next[k];
    onChange(next);
  };
  const interactive = grant.startsWith("authorization_code");
  const grantId = useId();
  const fields: Field[] = [];
  if (grant === "") {
    fields.push({ key: "accessToken", label: "Access token", secret: true, placeholder: "{{accessToken}}" });
  } else {
    if (interactive) fields.push({ key: "authUrl", label: "Auth URL", placeholder: "https://idp.example.com/authorize" });
    fields.push({ key: "accessTokenUrl", label: "Access token URL", placeholder: "https://idp.example.com/oauth/token" });
    fields.push({ key: "clientId", label: "Client ID" });
    fields.push({ key: "clientSecret", label: "Client secret", secret: true, placeholder: grant === "authorization_code_with_pkce" ? "optional for public clients" : "" });
    fields.push({ key: "scope", label: "Scope", placeholder: "space-separated" });
    if (grant === "password_credentials") {
      fields.push({ key: "username", label: "Username" }, { key: "password", label: "Password", secret: true });
    }
    if (interactive) fields.push({ key: "redirect_uri", label: "Callback URL", placeholder: `${location.origin}/oauth2/callback` });
  }
  return (
    <>
      <div className="field-row">
        <label className="field-label" htmlFor={grantId}>
          Grant type
        </label>
        <Select
          id={grantId}
          className="wide"
          value={grant}
          onChange={(v) => {
            const next = { ...params };
            if (v) next.grant_type = v;
            else delete next.grant_type;
            onChange(next);
          }}
          options={GRANTS.map((g) => ({ value: g.id, label: g.label }))}
        />
      </div>
      {fields.map((f) => {
        const Field = f.secret ? Input.Password : Input;
        return (
          <label key={f.key} className="field-row">
            <span className="field-label">{f.label}</span>
            <Field
              className="mono wide"
              value={params[f.key] ?? ""}
              onChange={(e) => set(f.key, e.target.value)}
              placeholder={f.placeholder}
              autoComplete="off"
              spellCheck={false}
              aria-label={f.label}
            />
          </label>
        );
      })}
      {grant !== "" && (
        <Collapse
          ghost
          size="small"
          className="oauth-advanced"
          items={[
            {
              key: "advanced",
              label: "Advanced",
              children: (
                <>
                  <div className="field-row">
                    <span className="field-label">Client auth</span>
                    <Select
                      className="wide"
                      aria-label="Client auth"
                      value={params.client_authentication ?? "header"}
                      onChange={(v) => set("client_authentication", v === "header" ? "" : v)}
                      options={[
                        { value: "header", label: "Basic auth header" },
                        { value: "body", label: "In the request body" },
                      ]}
                    />
                  </div>
                  {grant === "authorization_code_with_pkce" && (
                    <div className="field-row">
                      <span className="field-label">PKCE method</span>
                      <Select
                        className="wide"
                        aria-label="PKCE method"
                        value={params.challengeAlgorithm ?? "S256"}
                        onChange={(v) => set("challengeAlgorithm", v === "S256" ? "" : v)}
                        options={[
                          { value: "S256", label: "SHA-256" },
                          { value: "plain", label: "Plain" },
                        ]}
                      />
                    </div>
                  )}
                  <label className="field-row">
                    <span className="field-label">Audience</span>
                    <Input className="mono wide" value={params.audience ?? ""} onChange={(e) => set("audience", e.target.value)} aria-label="Audience" />
                  </label>
                  <label className="field-row">
                    <span className="field-label">Refresh URL</span>
                    <Input
                      className="mono wide"
                      value={params.refreshTokenUrl ?? ""}
                      onChange={(e) => set("refreshTokenUrl", e.target.value)}
                      placeholder="same as the access token URL"
                      aria-label="Refresh token URL"
                    />
                  </label>
                </>
              ),
            },
          ]}
        />
      )}
      <div className="field-row">
        <span className="field-label">Add token to</span>
        <Select
          className="wide"
          aria-label="Add token to"
          value={params.addTokenTo ?? "header"}
          onChange={(v) => set("addTokenTo", v === "header" ? "" : v)}
          options={[
            { value: "header", label: "Authorization header" },
            { value: "queryParams", label: "access_token query parameter" },
          ]}
        />
      </div>
      {(params.addTokenTo ?? "header") === "header" && (
        <label className="field-row">
          <span className="field-label">Header prefix</span>
          <Input className="mono wide" value={params.headerPrefix ?? ""} onChange={(e) => set("headerPrefix", e.target.value)} placeholder="Bearer" aria-label="Header prefix" />
        </label>
      )}
      {grant !== "" && context && <TokenPanel context={context} />}
    </>
  );
}

export interface TokenContext {
  path?: string;
  request: PyroRequest;
}

/** Shows the cached token for a request's OAuth 2.0 settings (own or inherited) and gets new ones. */
export function TokenPanel({ context, inheritedOnly = false }: { context: TokenContext; inheritedOnly?: boolean }) {
  const environment = useApp((s) => s.environment);
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [show, setShow] = useState(false);
  const body = { path: context.path, request: context.request, environment: environment ?? undefined };
  const key = ["oauth2", context.path, JSON.stringify(context.request.auth ?? null), environment];
  const status = useQuery({ queryKey: key, queryFn: () => api.oauthToken(body), retry: false });

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin === location.origin && e.data?.pyroOAuth) {
        qc.invalidateQueries({ queryKey: ["oauth2"] });
        setBusy(false);
        if (e.data.pyroOAuth !== "ok") setError("Sign-in failed; see the sign-in window.");
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [qc]);

  if (status.error) return <Alert className="token-error" type="error" showIcon title={status.error.message} />;
  const s = status.data;
  // Inherited auth that isn't OAuth 2.0: nothing to show.
  if (!s || !s.oauth) return null;

  const getToken = async () => {
    setError(null);
    setBusy(true);
    try {
      if (s.interactive) {
        const { url } = await api.oauthAuthorize(body);
        const win = window.open(url, "pyro-oauth", "width=520,height=720");
        if (!win) throw new Error("The sign-in window was blocked; allow pop-ups for Pyro.");
        // Fall back to polling in case the window can't message back.
        const timer = setInterval(() => {
          if (win.closed) {
            clearInterval(timer);
            setBusy(false);
            qc.invalidateQueries({ queryKey: ["oauth2"] });
          }
        }, 800);
        return;
      }
      qc.setQueryData(key, await api.oauthToken({ ...body, fetch: true }));
      setBusy(false);
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  };
  const clear = async () => {
    qc.setQueryData(key, await api.oauthToken({ ...body, clear: true }));
  };

  const t = s.token;
  return (
    <div className="token-panel">
      <div className="token-head">
        <KeyRound size={14} aria-hidden />
        <strong>{inheritedOnly || s.inherited ? "OAuth 2.0 token (inherited settings)" : "Access token"}</strong>
        <span className="muted small">{t ? tokenAge(t.expiresAt, s.expired) : "none yet"}</span>
      </div>
      {t && (
        <div className="token-value">
          <code className="mono">{show ? t.accessToken : `${t.accessToken.slice(0, 6)}…${t.accessToken.slice(-4)}`}</code>
          <Tooltip title={show ? "Hide token" : "Show token"}>
            <Button type="text" icon={show ? <EyeOff size={13} /> : <Eye size={13} />} onClick={() => setShow(!show)} aria-label={show ? "Hide token" : "Show token"} />
          </Tooltip>
          {t.scope && <span className="muted small">scope: {t.scope}</span>}
        </div>
      )}
      {s.unresolved && s.unresolved.length > 0 && <Alert type="warning" showIcon title={`Unresolved variables: ${s.unresolved.join(", ")} (pick an environment?)`} />}
      <div className="token-actions">
        <Button icon={s.interactive ? <LogIn size={13} /> : <RefreshCw size={13} />} onClick={getToken} disabled={busy}>
          {busy ? (s.interactive ? "Waiting for sign-in…" : "Getting token…") : "Get new access token"}
        </Button>
        {t && (
          <Button icon={<Trash2 size={13} />} onClick={clear}>
            Forget
          </Button>
        )}
      </div>
      <p className="muted small">
        {s.interactive ? (
          <>
            Sign-in opens in a new window. Register <code>{s.redirectUri}</code> as a redirect URI with your provider. Tokens are kept in memory while Pyro
            runs and refreshed automatically when the provider issues refresh tokens.
          </>
        ) : (
          <>Sends fetch a token automatically and reuse it until it expires; this button forces a new one.</>
        )}
      </p>
      {error && <Alert type="error" showIcon title={error} />}
    </div>
  );
}

function tokenAge(expiresAt?: string, expired?: boolean) {
  if (!expiresAt) return "no expiry given";
  if (expired) return "expired";
  const mins = Math.round((new Date(expiresAt).getTime() - Date.now()) / 60000);
  return mins >= 120 ? `expires in ${Math.round(mins / 60)} h` : `expires in ${Math.max(mins, 0)} min`;
}
