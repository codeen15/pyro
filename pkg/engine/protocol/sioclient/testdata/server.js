// Conformance server for sioclient's TestRealServer.
//   npm install socket.io@4 && node server.js 3999 && node server.js 3998 polling
//   PYRO_SIO_URL=http://127.0.0.1:3999 PYRO_SIO_POLLING_URL=http://127.0.0.1:3998 \
//     go test ./pkg/engine/protocol/sioclient/
// With "polling" the server refuses WebSockets, like a proxy that blocks them.
const { Server } = require("socket.io");
const port = Number(process.argv[2] || 3999);
const opts = process.argv[3] === "polling" ? { transports: ["polling"] } : {};
if (process.env.PING_MS) Object.assign(opts, { pingInterval: Number(process.env.PING_MS), pingTimeout: Number(process.env.PING_MS) });
const io = new Server(port, opts);
io.on("connection", (socket) => {
  socket.emit("welcome", socket.handshake.auth.token, socket.conn.transport.name);
  socket.on("echo", (...args) => {
    const ack = typeof args[args.length - 1] === "function" ? args.pop() : null;
    socket.emit("echo", ...args);
    if (ack) ack("ok", args.length);
  });
  // Binary: echo the buffer back with its length, and ack with a buffer.
  socket.on("bin", (buf, meta, ack) => {
    socket.emit("bin", buf, { len: buf.length, meta });
    if (typeof ack === "function") ack(Buffer.from([9, 8, 7]));
  });
  socket.on("sendme", () => socket.emit("file", { name: "a.bin", data: Buffer.from("hi there") }));
});
io.of("/admin").use((socket, next) => next(new Error("admins only")));
