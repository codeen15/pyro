pyro.registerProtocol("js-throws", (ctx, req) => {
  throw new Error("boom")
})
