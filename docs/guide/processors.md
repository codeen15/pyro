# Processors and tests

Like JMeter, Pyro lets you run steps around each request: **pre-processors** before it's
sent, **post-processors** after the response arrives, and a **timer** (think time) before
it in load tests. They run in manual sends, collection runs and load tests alike, except
the timer.

Each request has a **Pre-processors** and a **Post-processors** tab. Collections and folders
have the same tabs in their settings; theirs apply to every request inside.

## Adding steps

Press **Add pre-processor** or **Add post-processor** and choose a type. Steps run top to
bottom. Every step card can be renamed, reordered with the arrows, switched off (it's kept
but skipped), or deleted.

| Type | Before | After | What it does |
|---|---|---|---|
| **Script** | ✓ | ✓ | Runs JavaScript with Postman's `pm.*` API (below). |
| **Set variables** | ✓ | ✓ | Sets each variable to a value; `{{variables}}` and `{{$functions}}` in values are filled in, in order. Like JMeter's User Parameters. |
| **Counter** | ✓ | | Sets a variable to *start* the first time, then adds *step* each time. Optionally wraps back after *end*, and formats with printf style (`%05d` gives `00001`). Each virtual user counts separately. |
| **Extract a value** | | ✓ | Copies part of the response into a variable (below). |
| **Assertion** | | ✓ | Checks the response (below). |
| Plugin processors | ✓ | ✓ | Steps added by installed plugins ([Plugins](plugins.md)). |

**Order:** before the request, the collection's steps run first, then each folder's (outer to
inner), then the request's. After the response the order is the same.

If a pre-processor fails (a script error, or a plugin that isn't available), the request isn't
sent, as in Postman. A failing post-processor is reported with the response.

## Assertions

| Check | Compares |
|---|---|
| Status code | The response status |
| Header | A header's value (give its name) |
| JSON path | A value in a JSON body, e.g. `$.data.items[0].id` |
| Body text | The whole body |
| Response time (ms) | How long the request took |

Operators: equals, not equals, contains, matches regex, `<`, `≤`, `>`, `≥`, exists, doesn't exist.
The path and the expected value can use `{{variables}}`, e.g. expect `$.id` to equal
`{{orderId}}` that an earlier request captured.
Results appear on the response's **Tests** tab. A failed assertion fails the request in
collection runs and counts toward the `checks` metric in load tests.

Assertions are cheaper than scripts, so prefer them in load tests.

## Extractors

| From | Path | Example |
|---|---|---|
| JSON path | A JSONPath expression | `$.token` |
| Header | A header name | `X-Request-Id` |
| Regex | A regular expression; the first group is used | `id=(\d+)` |
| Boundary | The text between a left and a right boundary | left `name="csrf" value="`, right `"` |
| XPath | An XPath 1.0 expression on an XML body | `//order/@id` |
| Status code | No path needed | |

The value goes into a runtime variable, so later requests can use `{{token}}`. Extracted
values are listed on the **Tests** tab. A path that matches nothing is reported there too.
Paths and boundaries can use `{{variables}}`.

## Scripts

Scripts are JavaScript, compatible with Postman's `pm.*` API, so imported Postman scripts keep
working. The most used parts:

```js
// Before the request
pm.variables.set("ts", Date.now());
pm.environment.get("baseUrl");
pm.request.headers.add({ key: "X-Trace", value: pm.variables.get("ts") });

// After the response
pm.test("status is 200", () => pm.response.to.have.status(200));
const body = pm.response.json();
pm.expect(body.items).to.have.lengthOf.at.least(1);
pm.collectionVariables.set("firstId", body.items[0].id);
console.log("got", body.items.length, "items");
```

Also available:
- `pm.globals`, `pm.collectionVariables`, `pm.environment`, `pm.variables`, `pm.info`;
- `pm.sendRequest`, and `pm.execution.skipRequest()`;
- `postman.setNextRequest` (in collection runs);
- the chai `expect` assertion library.

`console.log` output appears under **Console** on the Tests tab. See
[postman-import.md](../postman-import.md) for exactly which `pm.*` features are supported.

Scripts written in Pyro's older form (the `scripts` field) still run; Pyro shows them as the
first Script step and saves them in the new form the next time you edit the list.

## Think time (timers)

On the **Pre-processors** tab, **Think time** sets how long a virtual user waits before this
request, like a JMeter timer:

| Type | Waits |
|---|---|
| Constant | the delay |
| Uniform random | the delay plus a random 0 to *range* |
| Gaussian random | the delay plus a normal deviation of *range* (never below zero) |
| Poisson random | the delay plus a Poisson-distributed wait with mean *range* |
| No think time | nothing, even if a folder or the collection sets a timer |
| Inherit | the nearest folder's timer, else the collection's |

Durations look like `500ms`, `1.5s` or `2m`; a bare number is milliseconds.

- **Where it applies:** load tests, and collection runs with **Apply think time** ticked (or
  `pyro run --think`). Manual sends never wait.
- **Metrics:** the wait isn't counted as response time.
- **Defaults:** set one for a whole collection or folder in its settings.
