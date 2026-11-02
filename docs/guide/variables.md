# Variables and environments

## Using variables

Write `{{name}}` anywhere in a request: URL, params, headers, body, auth fields, WebSocket and
Socket.IO messages, processor fields. The value is filled in when the request is sent.

In the editor, variables are coloured where you type them:

| Colour | Meaning |
|---|---|
| Green | Defined; this value will be used |
| Red | Not defined anywhere; it will be sent as written, and the response shows a warning |
| Purple | A dynamic value generated on every send (see below) |

**Hover a variable** to see its value and where it comes from. You can change the value there
and press **Save**; it's written to that scope's file. For a red (missing) variable, choose
where to add it (the environment, the folder, the collection or globals), type a value and
press **Add**.

## Where values come from

When a name is defined in several places, the first match in this list wins:

| Scope | Set by | Saved in |
|---|---|---|
| **Runtime** | Scripts, extractors and processors while you work or during a run | Memory only |
| **Data** | The current row of a data file in a collection run or load test | The data file |
| **Environment** | The environment picked in the top bar | `environments/<name>.env.yaml` |
| **Folder** | A folder's settings (inner folders first) | `folder.yaml` |
| **Collection** | The collection's settings | `collection.yaml` |
| **Globals** | Environments and globals page | `globals.yaml` |

## Environments and globals

An **environment** is a named set of values, such as `local`, `staging` or `production`. Pick
one in the top bar; each environment gets a coloured dot (one whose name contains *prod* is
always red, as a warning).

Press the sliders button in the top bar to open **Environments and globals**:

- **Globals** apply in every collection and environment.
- Create an environment with **New environment**, rename it in place, delete it from its page,
  or press **Use this environment** to select it.
- Each variable has an **initial value** (saved in the file, shared through git) and a
  **current value** (only for this Pyro session, never written to disk). Use current values
  for personal tokens. **Save current value as initial** copies one into the file; the reset
  button drops it.
- Mark a variable **secret** to mask it on screen. Secret values are still saved in the file,
  so keep real credentials in current values or out of shared workspaces.

Collection and folder variables are edited on their **Variables** settings tab.

## Dynamic values

These are generated fresh on every send:

| Name | Value |
|---|---|
| `{{$guid}}`, `{{$randomUUID}}` | A random UUID |
| `{{$timestamp}}` | Unix time in seconds |
| `{{$isoTimestamp}}` | The current time, ISO 8601 |
| `{{$randomInt}}` | A random integer 0–1000 |
| `{{$randomBoolean}}` | `true` or `false` |
| `{{$randomAlphaNumeric}}` | One random letter or digit |
| `{{$randomHexadecimalColor}}` | A random `#rrggbb` colour |
| `{{$randomEmail}}`, `{{$randomUserName}}`, `{{$randomPassword}}`, `{{$randomIP}}` | Random values of that kind |

## Cookies

Cookies set by responses are kept per domain, as a browser would, and sent with later
requests. Press the cookie button in the top bar to see them. You can edit a value
(press `Enter` to save), add a cookie, delete one, or clear them all. In load tests every
virtual user has its own cookie jar.
