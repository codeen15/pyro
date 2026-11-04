# Keyboard shortcuts and tips

## Shortcuts

| Keys | Does |
|---|---|
| `Ctrl` + `Enter` (`⌘` + `Enter` on macOS) | Send the request, or connect / send for WebSocket and Socket.IO |
| `Ctrl` + `S` (`⌘` + `S`) | Save the open request, load test, or collection or folder settings |
| `↑` / `↓` on the divider | Resize the request and response panes |

## Theme

The **theme menu** in the status bar switches between System (follows your OS), Light and
Dark. The choice is remembered per browser.

## Tips

- **Hover any `{{variable}}`** to see its value and fix a missing one on the spot.
- **Long names in the sidebar** are cut off at the end of the row; hover one to read it in full.
- **Same base URL everywhere:** put it in a collection variable (`{{baseUrl}}`) and override it
  per environment.
- **Personal tokens:** set them as *current values* on the environment page; they never reach
  disk or git.
- **Logging in once per virtual user:** in a load test, tick **Once per VU** on the login step
  and extract the token into a variable.
- **Realistic pacing:** give requests a think time (Pre-processors tab) instead of adding think
  steps everywhere; set a default on the collection.
- **Before a big test,** run the **Smoke** profile to check the flow and thresholds.
- **Assertions beat scripts under load:** they're much cheaper to run.

## Troubleshooting

| Problem | Try |
|---|---|
| "This tab isn't connected to Pyro" | Open the link `pyro serve` printed; it carries the session token |
| "Engine unreachable" in the status bar | `pyro serve` stopped; start it again |
| A variable is sent as `{{name}}` | It's red in the editor: define it, or pick the right environment |
| A load test asks to confirm hosts | Expected for non-local hosts; add trusted ones to `loadAllowHosts` in `pyro.yaml` |
| Latency looks worse than expected under load | Check the CPU warning; spread the load over [agents](distributed.md) |
| A plugin request says "not installed" | Install and approve the plugin ([Plugins](plugins.md)) |
| A JMeter import's data file is missing | Upload it on the load test's **Test data** panel, or import with `pyro import` |
