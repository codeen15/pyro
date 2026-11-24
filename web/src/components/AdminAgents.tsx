import { useState } from "react";
import { Alert, App as AntApp, Button, Dropdown, Input, Modal, Switch, Table, Tag, Typography } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Copy, Plus, TriangleAlert } from "../icons";
import { team, type AdminAgent, type AgentConnectInfo } from "../api";
import { dockerCommand, imageTag, plainCommand } from "../agentCommand";
import { StatusDot, statusText } from "./AgentStatus";
import { CopyField, useCopy } from "./CopyField";

// Admin → Agents: the load agents this team server can run tests on. Direct
// agents are dialled by the server; connecting ones dial in with their own token.

function StatusCell({ a }: { a: AdminAgent }) {
  return (
    <div className="agent-status">
      <span className="agent-status-line">
        <StatusDot status={a.status} />
        <span>{statusText(a)}</span>
      </span>
      {a.detail && a.status !== "available" && <span className="muted small agent-detail">{a.detail}</span>}
    </div>
  );
}

export function AdminAgents({ agents, loading, error }: { agents: AdminAgent[] | undefined; loading: boolean; error?: string }) {
  const qc = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const [direct, setDirect] = useState<AdminAgent | "new" | null>(null);
  const [connecting, setConnecting] = useState<{ id?: string; name: string; info?: AgentConnectInfo } | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin-agents"] });

  const test = async (a: AdminAgent) => {
    try {
      const r = await team.adminTestAgent(a.id);
      if (r.status === "available" || r.status === "busy") message.success(`${a.name} is ${r.status}`);
      else message.warning(`${a.name} is ${r.status}${r.detail ? `: ${r.detail}` : ""}`);
      refresh();
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const remove = (a: AdminAgent) =>
    modal.confirm({
      title: `Remove “${a.name}”?`,
      content: "Its token stops working and a connected agent is disconnected. Load tests can't use it any more.",
      okText: "Remove",
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await team.adminDeleteAgent(a.id);
          message.success(`Removed ${a.name}`);
          refresh();
        } catch (e) {
          message.error((e as Error).message);
        }
      },
    });
  const rotate = (a: AdminAgent) =>
    modal.confirm({
      title: `Rotate the token for “${a.name}”?`,
      content: "The current token stops working and the connected agent is dropped. You get a new command to start it with.",
      okText: "Rotate",
      onOk: async () => {
        try {
          const info = await team.adminRotateAgent(a.id);
          setConnecting({ id: a.id, name: a.name, info });
          refresh();
        } catch (e) {
          message.error((e as Error).message);
        }
      },
    });

  return (
    <div className="admin-agents">
      <div className="admin-agents-head">
        <p className="muted small">Load agents run distributed load tests. Add one the server can reach, or one that connects in from behind a firewall.</p>
        <Dropdown
          trigger={["click"]}
          menu={{
            items: [
              { key: "direct", label: "Direct…" },
              { key: "connect", label: "Connects in…" },
            ],
            onClick: ({ key }) => (key === "direct" ? setDirect("new") : setConnecting({ name: "" })),
          }}
        >
          <Button type="primary" icon={<Plus size={14} />}>
            Add agent <ChevronDown size={12} />
          </Button>
        </Dropdown>
      </div>
      {error && <Alert type="error" showIcon title={error} />}
      <Table<AdminAgent>
        rowKey="id"
        size="small"
        pagination={false}
        scroll={{ x: "max-content" }}
        loading={loading}
        locale={{ emptyText: "No agents yet." }}
        dataSource={agents ?? []}
        columns={[
          { title: "Name", render: (_, a) => <strong>{a.name}</strong> },
          { title: "How", width: 120, render: (_, a) => <Tag>{a.mode === "direct" ? "Direct" : "Connects in"}</Tag> },
          {
            title: "Address",
            render: (_, a) =>
              a.fromFlags ? (
                <span className="muted">from server flags</span>
              ) : (
                <span className="agent-address">
                  <span className="mono">{a.address || "—"}</span>
                  {a.mode === "direct" && a.address && !a.encrypted && (
                    <Tag icon={<TriangleAlert size={12} />} variant="outlined">
                      not encrypted
                    </Tag>
                  )}
                </span>
              ),
          },
          { title: "Status", render: (_, a) => <StatusCell a={a} /> },
          { title: "Version", width: 90, render: (_, a) => a.version || "—" },
          { title: "CPUs", width: 70, align: "right", render: (_, a) => a.cpus || "—" },
          { title: "Max VUs", width: 90, align: "right", render: (_, a) => (a.maxVUs ? a.maxVUs.toLocaleString() : "—") },
          {
            title: <span className="sr-only">Actions</span>,
            align: "right",
            render: (_, a) =>
              a.fromFlags ? (
                <span className="muted small">read-only</span>
              ) : (
                <span className="agent-actions">
                  {a.mode === "direct" ? (
                    <>
                      <Button size="small" onClick={() => test(a)} aria-label={`Test ${a.name}`}>
                        Test
                      </Button>
                      <Button size="small" onClick={() => setDirect(a)} aria-label={`Edit ${a.name}`}>
                        Edit
                      </Button>
                    </>
                  ) : (
                    <Button size="small" onClick={() => rotate(a)} aria-label={`Rotate token for ${a.name}`}>
                      Rotate
                    </Button>
                  )}
                  <Button size="small" danger onClick={() => remove(a)} aria-label={`Remove ${a.name}`}>
                    Remove
                  </Button>
                </span>
              ),
          },
        ]}
      />
      {direct && <DirectModal agent={direct === "new" ? undefined : direct} onClose={() => setDirect(null)} onSaved={refresh} />}
      {connecting && <ConnectModal start={connecting} agents={agents} onClose={() => setConnecting(null)} onCreated={refresh} />}
    </div>
  );
}

/** Add or edit an agent the server dials. */
function DirectModal({ agent, onClose, onSaved }: { agent?: AdminAgent; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(agent?.name ?? "");
  const [address, setAddress] = useState(agent?.address ?? "");
  const [token, setToken] = useState("");
  const [insecure, setInsecure] = useState(agent?.insecureTLS ?? false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState<AdminAgent | null>(null);
  const plain = /^http:\/\//i.test(address.trim());
  const ready = name.trim() && address.trim() && (agent || token);

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      // Saving probes the agent once, so the reply says whether the address and token work.
      const r = agent
        ? await team.adminUpdateAgent(agent.id, {
            name: name.trim(),
            address: address.trim(),
            insecureTLS: insecure,
            ...(token ? { token } : {}),
          })
        : (await team.adminAddAgent({ name: name.trim(), mode: "direct", address: address.trim(), token, insecureTLS: insecure })).agent;
      setSaved(r);
      onSaved();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      title={saved ? `${saved.name} saved` : agent ? `Edit ${agent.name}` : "Add an agent the server connects to"}
      onCancel={onClose}
      footer={
        saved ? (
          <Button type="primary" onClick={onClose}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button type="primary" loading={busy} disabled={!ready} onClick={save}>
              {agent ? "Save and test" : "Add and test"}
            </Button>
          </>
        )
      }
    >
      {saved ? (
        saved.status === "available" || saved.status === "busy" ? (
          <Alert
            type="success"
            showIcon
            title="The agent answered"
            description={`${saved.version ? `Version ${saved.version}` : "Reachable"}${saved.cpus ? `, ${saved.cpus} CPUs` : ""}.`}
          />
        ) : (
          <Alert
            type="warning"
            showIcon
            title="Saved, but the agent can't be reached yet"
            description={`${saved.detail || `Its status is ${saved.status}`}. Check the address and token with Edit, or start the agent; the server keeps trying.`}
          />
        )
      ) : (
        <>
          {err && <Alert className="modal-alert" type="error" showIcon title={err} />}
          <label className="modal-field">
            <span>Name</span>
            <Input autoFocus aria-label="Name" placeholder="e.g. vps-sg" maxLength={64} value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="modal-field">
            <span>Address</span>
            <Input aria-label="Address" placeholder="10.0.0.5:7979 or https://agent1.example.com" value={address} onChange={(e) => setAddress(e.target.value)} />
            {plain && <span className="muted small">http:// is not encrypted: the token and test data cross the network in clear text. Fine on a private network.</span>}
          </label>
          <label className="modal-field">
            <span>Token</span>
            <Input.Password
              aria-label="Token"
              autoComplete="new-password"
              placeholder={agent ? "Leave empty to keep the current token" : "The agent's --token"}
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
            <span className="muted small">Stored encrypted. The server needs it in clear text to call the agent, so anyone with the server's secret can read it.</span>
          </label>
          <label className="modal-field modal-switch">
            <Switch aria-label="Skip certificate check" checked={insecure} onChange={setInsecure} />
            <span>Skip certificate check (for self-signed certificates)</span>
          </label>
        </>
      )}
    </Modal>
  );
}

/** Add a connecting agent, or rotate its token: shows the commands once and waits for the agent. */
function ConnectModal({
  start,
  agents,
  onClose,
  onCreated,
}: {
  start: { id?: string; name: string; info?: AgentConnectInfo };
  agents: AdminAgent[] | undefined;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [name, setName] = useState(start.name);
  const [id, setId] = useState(start.id);
  const [info, setInfo] = useState(start.info);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  // The address the agent should dial. The server's own guess is kept out of the field when it
  // refused to use it (a warning), so a hostile Host header never lands in a command by itself.
  const [base, setBase] = useState(start.info && !start.info.warning ? start.info.base : "");

  const create = async () => {
    setBusy(true);
    setErr("");
    try {
      const r = await team.adminAddAgent({ name: name.trim(), mode: "connect" });
      if (!r.token || !r.command) throw new Error("The server didn't return a token");
      const next = { token: r.token, base: r.base ?? "", command: r.command, warning: r.warning };
      setId(r.agent.id);
      setInfo(next);
      setBase(next.warning ? "" : next.base);
      onCreated();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const live = id ? agents?.find((a) => a.id === id) : undefined;
  const connected = live?.status === "available" || live?.status === "busy";

  return (
    <Modal
      open
      width={720}
      title={info ? `Start “${name}”` : "Add an agent that connects in"}
      onCancel={onClose}
      footer={
        info ? (
          <Button type="primary" onClick={onClose}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button type="primary" loading={busy} disabled={!name.trim()} onClick={create}>
              Create agent
            </Button>
          </>
        )
      }
    >
      {info ? (
        <ConnectCommands info={info} base={base} setBase={setBase} connected={connected} />
      ) : (
        <>
          {err && <Alert className="modal-alert" type="error" showIcon title={err} />}
          <label className="modal-field">
            <span>Name</span>
            <Input
              autoFocus
              aria-label="Name"
              placeholder="e.g. office-1"
              maxLength={64}
              value={name}
              onChange={(e) => setName(e.target.value)}
              onPressEnter={() => name.trim() && create()}
            />
            <span className="muted small">You get a command to run on the machine; the agent then connects to this server by itself.</span>
          </label>
        </>
      )}
    </Modal>
  );
}

function ConnectCommands({ info, base, setBase, connected }: { info: AgentConnectInfo; base: string; setBase: (s: string) => void; connected: boolean }) {
  const copy = useCopy();
  const edited = base.trim() !== "" && !(base.trim() === info.base && !info.warning);
  // Until the address is edited the server's own command is shown as it came.
  const address = base.trim();
  const docker = edited ? dockerCommand(imageTag(info.command), address, info.token) : info.command;
  const plain = plainCommand(edited ? address : info.warning ? "<server-url>" : info.base, info.token);
  const shaky = address !== "" && !/^https?:\/\/[^\s']+$/.test(address);

  return (
    <div className="invite-result agent-connect">
      <Alert type="warning" showIcon title="This is the only time the token is shown. Copy a command now." />
      {info.warning && <Alert type="info" showIcon title={info.warning} />}
      <label className="modal-field">
        <span>Server address</span>
        <Input
          aria-label="Server address"
          status={shaky ? "warning" : undefined}
          placeholder={info.warning ? "https://pyro.example.com" : undefined}
          value={base}
          onChange={(e) => setBase(e.target.value)}
        />
        <span className="muted small">
          Where the agent reaches this server: the address you use for this page, with the same port, or a public address if the agent is outside your network.
          {shaky && " It should look like http://host:port or https://host."}
        </span>
      </label>
      <label className="modal-field">
        <span>Agent token</span>
        <CopyField label="Agent token" value={info.token} />
      </label>
      <div className="modal-field">
        <span>With Docker</span>
        <div className="agent-command-row">
          <pre className="agent-command mono" aria-label="Docker command" tabIndex={0}>
            {docker}
          </pre>
          <Button icon={<Copy size={14} />} aria-label="Copy the Docker command" onClick={() => copy(docker)}>
            Copy
          </Button>
        </div>
      </div>
      <div className="modal-field">
        <span>With the pyro program</span>
        <div className="agent-command-row">
          <pre className="agent-command mono" aria-label="Plain command" tabIndex={0}>
            {plain}
          </pre>
          <Button icon={<Copy size={14} />} aria-label="Copy the plain command" onClick={() => copy(plain)}>
            Copy
          </Button>
        </div>
      </div>
      <p className="agent-wait" role="status">
        {connected ? (
          <>
            <StatusDot status="available" /> <Typography.Text>Connected</Typography.Text>
          </>
        ) : (
          <>
            <StatusDot status="offline" /> <Typography.Text>Waiting for the agent to connect…</Typography.Text>
          </>
        )}
      </p>
    </div>
  );
}

