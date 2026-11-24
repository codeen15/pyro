// Returns the wall clock and a random number, to check plugins get the
// real ones (not WASI's fixed clock and deterministic random source).
pyro.registerProtocol("js-clock", (ctx, req) => {
  return { status: 0, body: JSON.stringify({ now: Date.now(), random: Math.random() }) }
})
