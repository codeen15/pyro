// Allocates req.options.mb (default 300) MiB in 1 MiB buffers and keeps them
// all until it returns.
pyro.registerProtocol("js-alloc", (ctx, req) => {
  const mb = Number(req.option("mb", "300"))
  const keep = []
  for (let i = 0; i < mb; i++) {
    const b = new Uint8Array(1 << 20)
    b[i] = 1
    keep.push(b)
  }
  return { status: 0, body: "allocated " + keep.length + " MiB" }
})
