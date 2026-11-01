// Conformance server for sioclient's TestRealServer.
//   npm install socket.io@4 && node server.js 3999
//   PYRO_SIO_URL=http://127.0.0.1:3999 go test ./pkg/engine/protocol/sioclient/
const { Server } = require("socket.io");
const io = new Server(Number(process.argv[2] || 3999));
io.on("connection", (socket) => {
  socket.emit("welcome", socket.handshake.auth.token);
  socket.on("echo", (...args) => {
    const ack = typeof args[args.length - 1] === "function" ? args.pop() : null;
    socket.emit("echo", ...args);
    if (ack) ack("ok", args.length);
  });
});
io.of("/admin").use((socket, next) => next(new Error("admins only")));
