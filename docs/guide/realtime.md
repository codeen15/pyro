# WebSocket and Socket.IO

WebSocket and Socket.IO requests work with a live connection instead of sending one request.
When you create one, you choose what it does:

| Action | What it does |
|---|---|
| **Connect** | Opens a named connection. The editor is a console: send messages and watch everything that goes each way. |
| **Send & receive** | Sends messages on a connection, then waits for a matching reply, an ack or a set time. |
| **Ping** | WebSocket only: sends a ping and measures the round trip to its pong. |
| **Close** | Closes a named connection, with an optional close code and reason. |

The action can't be changed later; requests made before actions existed are Connect requests.
Splitting a conversation into steps lets HTTP requests run in between: open a connection, log
in over HTTP, then send the token on the socket and wait for the answer before the next
request. See [Steps on a connection](#steps-on-a-connection).

## Connecting

1. Create a request of type **WebSocket** or **Socket.IO** ([Requests](requests.md)).
2. Enter the URL: `wss://…` or `ws://…` for WebSocket, or the server's `http(s)://` address
   for Socket.IO.
3. Optionally give it a **Connection name** (default `default`). Send, ping and close requests
   reach the connection by this name.
4. Press **Connect** (`Ctrl` + `Enter`). The message log turns green while connected.
   **Disconnect** closes the connection.

The connection stays open when you move to other requests, so steps and HTTP requests can use
it. Coming back to the Connect request picks the console up again, with every message the
steps sent and received in the log.

Tabs:

| Tab | What it holds |
|---|---|
| **Message** | The message composer |
| **Saved** | Messages saved with **Save message**; **Load** puts one back in the composer |
| **Headers** | Headers sent with the handshake |
| **Auth** | The same auth types as HTTP (applied to the handshake) |
| **Socket.IO** | Socket.IO only: namespace, path (default `/socket.io/`), auth JSON sent with CONNECT, and the transport |

The message in the composer is part of the request: **Save** (`Ctrl` + `S`) keeps it, so it's
there next time you open the request. **Save message** adds it to the **Saved** list as well.

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

## Steps on a connection

Send & receive, Ping and Close requests act on the connection whose name they give. Its
connect request must have run first: by hand (press Connect), earlier in a
[collection run](runner.md), or earlier in a load test VU's flow.

A step with an empty connection name (choose **Use its own connection**) needs its own URL,
headers, auth and Socket.IO settings: it connects, runs and disconnects by itself. A Close
with no name closes `default`.

### Send & receive

**Messages** holds the messages to send, in order: an event, typed arguments and **Ask for an
ack** for Socket.IO, a Text, JSON or Binary payload for WebSocket. `{{variables}}` are filled
in when the step runs, so a value an earlier HTTP request extracted (`{{token}}`) can go in a
message.

**Wait** decides when the next request starts:

| Wait for | Ends when | Body of the step |
|---|---|---|
| Nothing | the messages are sent | empty |
| **A message that matches** | a received message meets every condition: Socket.IO **event**, **contains** text, **matches regex**, and a **JSONPath** that exists (and **equals** a value, if given). With no conditions, the next message. | the matched message |
| **The ack** (Socket.IO) | the server acknowledges the last message sent with **Ask for an ack** | the ack's arguments |
| **A set time** | the time is up | a JSON array of every message received |

**Timeout** (default `10s`) limits a message or ack wait; without a match in time, the step
fails with an error such as *no message containing "ready" within 10s*. Messages that arrived
on the connection since the last step count too, so a reply that came while an HTTP request
ran isn't missed. Messages already seen by an earlier step are not matched again.

Post-processors (assertions, extractors, scripts) run on the step's body as if it were a
response, so `{"type":"welcome","room":"r1"}` can be checked with a JSONPath assertion or its
`room` extracted into `{{room}}`. The result panel shows the matched message as a JSON tree,
every message sent and received on the **Messages** tab, and test results.

### Ping and Close

**Ping** sends a WebSocket ping and reports the round trip. Socket.IO connections keep
themselves alive with the server's heartbeat; to measure them, send a message that asks for an
ack and wait for it.

**Close** takes an optional **close code** (1000, or 3000–4999 for applications) and
**reason** (up to 123 bytes); both apply to WebSocket. The Connect request's console shows the
connection as disconnected.

### An example

```
chat/
  open.req.yaml      Connect, connection "chat"
  login.req.yaml     HTTP POST /login, extracts {{token}}
  join.req.yaml      Send & receive on "chat": {"type":"join","token":"{{token}}"},
                     wait for a message with $.type = "welcome"
  leave.req.yaml     Close "chat"
```

Run the folder in the [collection runner](runner.md), or press Connect on `open` and run the
others one by one.

## In load tests

WebSocket and Socket.IO requests can be steps in a load test. A connect request with
**Keep open** connects, sends a list of messages and keeps the connection open for a set time.
Without Keep open or messages to send, it opens the named connection for the VU, and the send, ping and close steps
after it in the flow use it, as in the runner. Each VU has its own connections. See
[Load testing](load-testing.md#websocket-and-socketio-steps).
