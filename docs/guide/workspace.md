# Workspace files

A workspace is a folder of plain files. Commit it to git to share collections, environments
and load tests with your team and review changes like code.

```
my-api/
  pyro.yaml                            # workspace settings
  globals.yaml                         # global variables
  collections/
    shop/
      collection.yaml                  # collection settings: variables, auth, processors…
      list-items.req.yaml              # a request
      orders/
        folder.yaml                    # folder settings (optional)
        create-order.req.yaml
  environments/
    staging.env.yaml
  loadtests/
    checkout-stress.load.yaml
  data/
    users.csv                          # test data for load tests and runs
```

- Each request is one `.req.yaml` file; folders are directories. The file name (without
  `.req.yaml`) is the request's path, e.g. `shop/orders/create-order`.
- A collection's `order:` lists children in run order. The importers fill it in; anything
  not listed follows, folders first, by name.
- Files are written atomically, so a crash never leaves half a file. Edits made outside Pyro
  show up after pressing reload in the sidebar.
- The exact formats are in [data-model.md](../data-model.md) and
  [load-engine.md](../load-engine.md).

**Not in the workspace** (personal, per machine):
- request history and saved load test runs, in `~/.pyro/pyro.db`;
- installed plugins, in `~/.pyro/plugins`;
- current (session) variable values;
- the UI theme.

## Settings in pyro.yaml

```yaml
name: My API
version: 1
loadAllowHosts: [staging.example.com]   # load test these without confirming each time
loadMaxRPS: 500                         # a ceiling for every load test (0 = none)
allowFilesOutsideWorkspace: false       # let request bodies read files outside the workspace
```

## Secrets

Values marked **secret** are masked in the UI but still saved in files. For credentials, use
**current values** (never written to disk), environment files you don't commit, or a
`.gitignore` entry. Generated API docs leave secret values out; Postman exports keep them but mark them as secret.
