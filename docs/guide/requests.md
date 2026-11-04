# Requests

## Collections and folders

A **collection** groups related requests (usually one API). Inside it, **folders** group
requests further and can be nested. Both are folders on disk, so the tree in the sidebar is
your workspace's `collections/` directory.

| To | Do this |
|---|---|
| Create a collection | Press **+** next to the search box in **Collections** |
| Add a request | Hover a collection or folder and press **+** |
| Add a folder | Hover a collection or folder, open **⋮** and choose **New folder in …** |
| Run everything inside | Hover it and press **▷** ([Collection runner](runner.md)) |
| Change its settings | Hover it and press the gear: variables, auth, processors, think time, mock server |
| Delete a request | The trash button in the request's header, or **⋮** on its row |
| Delete a folder or collection | **⋮** on its row, then **Delete …** |
| Reload from disk | The refresh button next to the search box, after editing files outside Pyro |

Deleting asks first and removes the files from the workspace.

**Settings inherited by every request inside:** a collection's or folder's settings page has
these tabs:
- **Overview:** name and a Markdown description (shown in generated API docs).
- **Variables:** see [Variables](variables.md).
- **Auth:** a request set to *Inherit* uses the nearest folder's auth, then the collection's.
- **Pre-processors** and **Post-processors**, with a default **think time**: see
  [Processors](processors.md).
- **Mock server** (collections only): see [Import, export, docs and mocks](import-export.md).

## Creating a request

Press **+** on a collection or folder. Choose the request type, give it a name and press
**Create request**:

| Type | For |
|---|---|
| **HTTP** | REST and GraphQL APIs |
| **WebSocket** | Raw WebSocket connections ([WebSocket and Socket.IO](realtime.md)) |
| **Socket.IO** | Socket.IO v4 servers ([WebSocket and Socket.IO](realtime.md)) |
| Plugin types | Types added by installed plugins, such as raw TCP ([Plugins](plugins.md)) |

WebSocket and Socket.IO requests also choose an action: **Connect**, **Send & receive**,
**Ping** (WebSocket) or **Close** ([Steps on a connection](realtime.md#steps-on-a-connection)).
The sidebar badge shows it (`WS`/`SIO` for connect, otherwise `SEND`, `PING`, `CLOSE`).

The type (and action) can't be changed later; it's shown as a label in the request header. To
use a different type, create a new request.

Rename a request by clicking its title. The line under it is the request's path in the
workspace (`collection/folder/name`), plus *unsaved* while you have changes. **Save**
(`Ctrl` + `S`) writes them to disk. Unsaved changes are kept while you move between requests,
and **Send** always uses what you see, saved or not.

## The HTTP editor

The URL bar holds the **method**, the **URL** and **Send**. `{{variables}}` in the URL are
highlighted ([Variables](variables.md)). Below it are tabs; a number shows how many entries
each has.

| Tab | What it holds |
|---|---|
| **Params** | Query parameters. Untick a row to keep it without sending it. A blank last row adds a new one. |
| **Headers** | Request headers, same table. |
| **Body** | None, JSON, Text, XML, Form URL-encoded, Multipart form, GraphQL or Binary file. JSON gets a **Format** button and a warning if it doesn't parse. In multipart forms the clip button turns a value into a file path. Binary file sends a file from the workspace. |
| **Auth** | Inherit, No auth, Basic, Bearer token, API key (header or query), Digest, OAuth 2.0, or a plugin auth type. |
| **Pre-processors** | Think time and steps that run before the request ([Processors](processors.md)). |
| **Post-processors** | Assertions, extractors and scripts that run after the response. |
| **Examples** | Saved responses, used by API docs and mock servers. |

**OAuth 2.0** supports client credentials, password, authorization code and authorization
code with PKCE. Press **Get new access token**: for browser sign-in a window opens and returns
to Pyro by itself. The token is cached and reused until it expires; tokens set on a collection
or folder are shared by every request that inherits that auth.

**File paths** (multipart file fields, binary bodies) are relative to the workspace folder.
For safety, files outside the workspace are refused unless `allowFilesOutsideWorkspace: true`
is set in `pyro.yaml` ([Workspace files](workspace.md)).

## The response

The response card shows:

- the **status**;
- the **time**, with a coloured swatch from the heat scale (cold steel under 100 ms up to
  white heat over 2 s). The scale is shown on the start page and in **Timing**, and a slow
  response keeps a glow until you open Timing;
- the **size** and **protocol**;
- a test summary.

| Tab | Shows |
|---|---|
| **Body** | **Pretty** shows JSON as a tree: click the arrows to fold or unfold any object or array (a folded one says how many keys or items it holds), or use **Expand all** / **Collapse all**. **Raw** shows the body as received. There's a copy button; images are shown; binary bodies are summarised. |
| **Tests** | Assertion and script results, extracted variables, and console output from scripts. |
| **Headers** / **Cookies** | What the server sent. |
| **Timing** | DNS, connect, TLS, send, wait and download, drawn to scale, plus whether the connection was reused. |

**Save as example** stores the response on the request's **Examples** tab, leaving out
per-response headers such as `Date`, and pretty-printing JSON.

Warnings appear above the response when a `{{variable}}` couldn't be resolved, when a script
failed, or when a pre-request script skipped the request.

## History

Every request you send is kept in **History** (in `~/.pyro/pyro.db`, not in the workspace),
with its status, time and age. Click an entry to see exactly what was sent and what came
back; entries for saved requests open the request. **Clear** empties the history.
