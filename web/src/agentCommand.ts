// The commands an administrator copies to start a connecting load agent.
// The server builds the Docker command when it creates the agent; the admin
// can then change the address, so the UI rebuilds it with the same quoting.

/** POSIX single-quoting: wraps in '…' and turns each ' into '\'' . */
export function shellQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

/** The image tag the server put in its command (`0.2.0`, `latest`). */
export function imageTag(serverCommand: string): string {
  return /ghcr\.io\/codeen15\/pyro:([^\s']+)/.exec(serverCommand)?.[1] ?? "latest";
}

/** The `docker run` command for an agent that dials `base` with `token`. */
export function dockerCommand(tag: string, base: string, token: string): string {
  return `docker run -d --name pyro-agent --restart unless-stopped ghcr.io/codeen15/pyro:${tag} agent --connect ${shellQuote(base)} --token ${shellQuote(token)}`;
}

/** The same agent started from a plain `pyro` binary. */
export function plainCommand(base: string, token: string): string {
  return `pyro agent --connect ${shellQuote(base)} --token ${shellQuote(token)}`;
}
