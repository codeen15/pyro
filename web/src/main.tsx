import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Result, Spin } from "antd";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/600.css";
import "./styles.css";
import { App } from "./App";
import { ThemeRoot } from "./theme";
import { handleSignedOut, setProject, team, type AuthConfig, type Me } from "./api";
import { setStorageScope } from "./store";
import { lastProject, projectFromURL, useTeam } from "./team";
import { AuthPage } from "./components/AuthPage";
import { InvitePage } from "./components/InvitePage";
import { ResetPage } from "./components/ResetPage";

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } },
});

/** Picks the project to open: the one in the URL, else the last one used. */
function chooseProject(me: Me): string | null {
  const all = me.workspaces.flatMap((w) => w.projects.map((p) => p.id));
  for (const id of [projectFromURL(), lastProject()]) if (id && all.includes(id)) return id;
  return all[0] ?? null;
}

type Boot = { state: "loading" } | { state: "signin"; config: AuthConfig } | { state: "ready" } | { state: "error"; message: string };

// A team server needs a signed-in user and an open project before the app
// renders; local Pyro renders straight away.
function TeamRoot({ config }: { config: AuthConfig }) {
  const [boot, setBoot] = useState<Boot>({ state: "loading" });
  const setTeam = useTeam((s) => s.set);

  useEffect(() => {
    handleSignedOut(() => setBoot({ state: "signin", config }));
    (async () => {
      if (!config.signedIn) {
        setBoot({ state: "signin", config });
        return;
      }
      try {
        const me = await team.me();
        const id = chooseProject(me);
        if (!id) {
          setBoot({ state: "error", message: "You don't have any projects. Ask a workspace owner to add you to one." });
          return;
        }
        // Keep /p/<id>/reports/<report> so the app can open that report.
        if (projectFromURL() !== id) window.history.replaceState(null, "", `/p/${encodeURIComponent(id)}`);
        try {
          localStorage.setItem("pyro.project", id);
        } catch {
          /* storage unavailable */
        }
        setProject(id);
        setStorageScope(id);
        setTeam({ me, project: await team.project(id) });
        setBoot({ state: "ready" });
      } catch (e) {
        if ((e as { status?: number }).status === 401) setBoot({ state: "signin", config });
        else setBoot({ state: "error", message: (e as Error).message });
      }
    })();
  }, [config, setTeam]);

  switch (boot.state) {
    case "loading":
      return <Spin fullscreen />;
    case "signin":
      // A full load after sign-in starts every query (and the event socket) clean.
      return <AuthPage config={boot.config} onSignedIn={() => window.location.reload()} />;
    case "error":
      return <Result status="warning" title="Can't open Pyro" subTitle={boot.message} />;
    default:
      return <App />;
  }
}

function render(config: AuthConfig | null) {
  const invite = config && /^\/invite\/([^/]+)/.exec(window.location.pathname);
  const reset = config && /^\/reset\/([^/]+)/.exec(window.location.pathname);
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <ThemeRoot>
          {reset ? (
            <ResetPage token={decodeURIComponent(reset[1])} />
          ) : invite ? (
            <InvitePage token={decodeURIComponent(invite[1])} config={config!} />
          ) : config ? (
            <TeamRoot config={config} />
          ) : (
            <App />
          )}
        </ThemeRoot>
      </QueryClientProvider>
    </StrictMode>,
  );
}

team.config().then(render);
