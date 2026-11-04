# Test reports

Every collection run and every load test is saved as a **report**: what ran, against which
environment, who ran it, when, and every result. Reports are for reviewing a run later,
spotting what changed since the last one, and filing evidence. You can save one as a PDF or
download it as Excel, CSV, JUnit or JSON.

## Finding reports

- **Reports** in the top bar (on a phone, under **More**) lists every report in the project,
  newest first.
- Each row shows the result (✓ Passed, ✗ Failed or ■ Stopped), the collection or load test,
  the outcome (requests and tests failed, or p95 and error rate for load tests), the
  environment, who ran it, when, and how long it took.
- **Filter** by kind (collection runs or load tests) and status, or search by name,
  environment or person.
- After a run, **Open report** on the runner goes straight to its report. A load test's result
  page is its report.

On a team server, everyone in the project sees its reports. Viewers can open and export
them; editors can also delete them.

## A collection run's report

The top shows the outcome: requests passed and failed, tests failed, and the pass rate.
Below that:

- **Details:** project, environment, who ran it, duration, iterations and the data file used.
- **Compared with the previous run:** what changed since the last saved run of the same
  collection or folder:
  - tests or requests that **now fail** or **now pass**;
  - requests that are new, or no longer run.

  **Open that run** shows the earlier report.
- **Failures:** each failed request with the assertions, script errors or connection errors
  that failed it.
- **All requests:** the full table. Click a row to see the request that was sent and its
  tests.

**Secrets are masked** before a report is saved:

- values of variables marked **secret** (in the environment, globals, collection or folders);
- the `Authorization`, `Cookie`, `Set-Cookie` and API key headers. The scheme stays readable,
  e.g. `Bearer ••••••`.

Response bodies are kept up to 64 KB each.

## Saving and sharing

| Button | Gives you |
|---|---|
| **Save as PDF** | Opens a printable page and the print dialog; choose "Save as PDF" |
| **Download → Excel workbook** | Sheets **Summary**, **Results** (one row per request per iteration) and **Failures**, with filters and frozen headers |
| **Download → CSV** | The results table, for spreadsheets and test management tools |
| **Download → JUnit XML** | For CI dashboards, Jenkins, Xray and other test management tools |
| **Download → JSON** | Everything in the report |
| **Copy link** (⧉) | The report's address, to share with people who can open the project |

A load test's **Download** menu gives an Excel workbook with sheets **Summary**, **Requests**
(count, failures, req/s and response time percentiles per request), **Thresholds** and
**Timeline** (one row per second). It also offers the per-request CSV, the full JSON result,
and the HTML report with charts.

Report links look like `https://pyro.example.com/p/<project>/reports/<id>` on a team server,
or `http://127.0.0.1:7878/reports/<id>` locally.

## From the command line and CI

`pyro run` and `pyro load` can write the same files:

```sh
pyro run payments -e staging --html report.html --xlsx report.xlsx --csv results.csv --junit junit.xml
pyro load checkout-stress -e staging --xlsx load.xlsx --csv requests.csv --html load.html
```

They can also **upload** the run to a team server, so it appears under Reports with the rest:

```sh
export PYRO_TOKEN=pyro_pat_…     # Account → API tokens on the team server
pyro run payments -e staging --report-to https://pyro.example.com --project 3f9c2a…
pyro load checkout-stress --report-to https://pyro.example.com --project 3f9c2a…
```

- `--project` is the ID in the project's address (`/p/<id>`).
- The report is recorded as run by the token's owner, and the command prints its link.
- A failed upload is a warning; the exit code still reflects the tests.

## Keeping reports

Reports live in Pyro's database:

- locally, `~/.pyro/pyro.db`;
- on a team server, its database (SQLite or PostgreSQL).

They aren't in the workspace files, so they don't clutter git. Delete reports you don't need
from the report page.
