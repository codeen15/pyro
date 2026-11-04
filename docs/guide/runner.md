# Collection runner

The runner sends every request in a collection or folder, in order, and reports which
passed. A request passes when it got a response, its scripts didn't fail and every
assertion held. Use it as a functional test suite, locally or in CI.

## From the app

Hover a collection or folder and press **▷** (Run all requests). Set:

| Option | Meaning |
|---|---|
| **Iterations** | How many times to run the whole list (default: one per data row, or 1) |
| **Delay** | Milliseconds to wait between requests |
| **Data file** | A CSV or JSON file; each iteration gets the next row as variables |
| **Stop at first failure** | End the run when a request fails |
| **Apply think time** | Wait each request's timer, as a load test would ([Processors](processors.md#think-time-timers)) |

Press **Run**. A progress bar and a table fill in live; click a row to see what was sent,
the response and its test results. Totals at the top read like *3 requests · 0 failed* and
*3 tests · 0 failed*. Every run is saved as a [report](reports.md): **Open report** shows it,
compared with the previous run, ready to save as PDF or Excel.

**Order:** requests run in tree order (folders follow the collection's `order`, see
[Workspace files](workspace.md)). A script can jump elsewhere with
`postman.setNextRequest("Request name")`, or stop the run with `setNextRequest(null)`.

**WebSocket and Socket.IO requests** run as steps: a Connect request opens its named
connection, and the send, ping and close requests after it use it, so HTTP requests can run in
between ([Steps on a connection](realtime.md#steps-on-a-connection)). Connections stay open
across iterations until a close step; any still open are closed when the run ends.

**Data files:** CSV with a header line, or a JSON array of objects. Each column (or key) is
available as `{{column}}` in that iteration.

## In CI

```sh
pyro run payments -e staging
pyro run payments/orders -d data/users.csv --junit results.xml
pyro run payments --json report.json --bail -q
```

`pyro run` exits with status 1 if any request or test fails, so a CI step fails with it.
`--junit` writes a JUnit XML report most CI systems can display; `--json` writes the full
result; `--html`, `--xlsx` and `--csv` write the [report](reports.md#from-the-command-line-and-ci)
for people; `--report-to` uploads it to a team server. Every flag is listed in
[Command line](cli.md#pyro-run).
