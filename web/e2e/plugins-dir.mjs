import { tmpdir } from "node:os";
import { join } from "node:path";

// The plugins folder e2e/server.mjs gives `pyro serve`; specs run `pyro plugin`
// commands against the same folder.
export const pluginsDir = join(tmpdir(), "pyro-e2e-plugins");
