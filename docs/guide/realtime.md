# WebSocket and Socket.IO

WebSocket and Socket.IO requests open a live connection instead of sending one request. The
editor is a console: connect, send messages, and watch everything that goes each way.

## Connecting

1. Create a request of type **WebSocket** or **Socket.IO** ([Requests](requests.md)).
2. Enter the URL: `wss://…` or `ws://…` for WebSocket, or the server's `http(s)://` address
   for Socket.IO.
3. Press **Connect** (`Ctrl` + `Enter`). The message log below turns green while connected.
   **Disconnect** closes the connection. Leaving the request closes it too.

Tabs:

| Tab | What it holds |
|---|---|
| **Message** | The message composer |
| **Saved** | Messages saved with **Save message**; **Load** puts one back in the composer |
| **Headers** | Headers sent with the handshake |
| **Auth** | The same auth types as HTTP (applied to the handshake) |
| **Socket.IO** | Socket.IO only: namespace, path (default `/socket.io/`), auth JSON sent with CONNECT, and the transport |

**Socket.IO transport:** the default tries WebSocket and falls back to HTTP long-polling when
a server or proxy refuses it. You can also force WebSocket only or long-polling only. The log
shows which one is in use.

## Sending Socket.IO events

A Socket.IO message is an **event** plus any number of **arguments**, as in Postman.

1. Type the event name.
2. Press **Add argument** for each argument and pick its type:

   | Type | Example | Sent as |
   |---|---|---|
   | Text | `lobby` | `"lobby"` |
   | JSON | `{"user": "{{user}}"}` | that object or array |
   | Number | `42` or `{{count}}` | a number |
   | Boolean | true, false, or a variable | `true` / `false` |
   | Binary | `AAEC/w==` (base64) | a binary attachment |

3. Reorder arguments with the arrows. Turn on the **JSON** switch to see or edit the whole
   argument list as the JSON array that's sent, e.g. `["lobby", {"user": "{{user}}"}, 42]`.
4. Tick **Ask for an ack** to have the server acknowledge it. The ack and its round-trip time
   show in the log.
5. Press **Send** (`Ctrl` + `Enter`).

A variable can stand in for a whole number, boolean or JSON argument (`{{count}}`); it is
filled in before the message is built.

## Sending WebSocket messages

A WebSocket message is one payload. Pick its format:

- **Text:** sent as is.
- **JSON:** checked as you type, with a **Format** button to pretty-print it.
- **Binary (base64):** the decoded bytes are sent as a binary frame.

## The message log

Each row shows the direction (sent or received), the time, the event, the payload and its
size. Click a row to expand it, with JSON pretty-printed. **Filter** narrows the log by text,
**Follow** keeps the newest message in view, and the trash button clears it. Received binary
attachments appear as `{"$binary": "<base64>"}` where they sat in the arguments.

## In load tests

WebSocket and Socket.IO requests can be steps in a load test. Each step connects, sends a
list of messages, and keeps the connection open for a set time. See
[Load testing](load-testing.md#websocket-and-socketio-steps).
