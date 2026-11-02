# Import, export, docs and mocks

## Importing

Press **Import** in the top bar. Drop files onto the page (or click to choose them), or paste
text such as a cURL command. Pyro detects the format:

| Format | Becomes |
|---|---|
| **Postman** collection (v2.0, v2.1) | A collection with folders, requests, auth, variables, scripts and saved examples |
| **Postman** environment or globals | An environment |
| **OpenAPI 3.x / Swagger 2.0** (JSON or YAML) | A collection with a request per operation, example bodies and the server URL as `{{baseUrl}}` |
| **HAR** (browser recording) | A collection of the recorded requests |
| **JMeter** test plan (`.jmx`) | A collection (a folder per thread group) **and** a load test with a scenario per thread group |
| **cURL** commands | Requests, as Chrome or Firefox "Copy as cURL" produces them |

**Add to** lets you put the imported requests into an existing collection or folder instead
of a new collection.

After importing, a report lists anything that couldn't be converted exactly, such as an
unsupported script call, a JMeter Groovy script, or a file path to copy. Click an entry to open
that request.

**JMeter imports** keep as much as they can:
- thread groups become executors;
- CSV Data Sets become scenario test data;
- timers become request think time with the same distribution;
- User Parameters and Counters become pre-processors;
- extractors and assertions become post-processors;
- controllers become flow steps.

Files referenced by a CSV Data Set can't be read from the browser. Upload them on the load
test's **Test data** panel, or import with `pyro import plan.jmx`, which copies them.
Every mapping is in [import-export.md](../import-export.md).

From the command line:

```sh
pyro import shop.postman_collection.json staging.postman_environment.json
pyro import openapi.yaml
pbpaste | pyro import - --into shop
```

## Exporting to Postman

Hover a collection, open **⋮** and choose **Export … to Postman**. A Postman v2.1 file
downloads. Assertions, extractors and processors become Postman test scripts. WebSocket,
Socket.IO and plugin requests, think time and plugin processors have no Postman equivalent;
they're left out and listed in a report.

```sh
pyro export shop -o shop.postman_collection.json
pyro export -e staging -o staging.postman_environment.json
```

## API documentation

Hover a collection, open **⋮** and choose **API docs for …**. Pyro opens a single
self-contained HTML page:
- each request with its Markdown description, parameters, body and saved examples;
- a ready-to-run cURL command.

Secret values and credentials are left out. Write descriptions on the collection's
**Overview** settings tab and on each request.

```sh
pyro docs shop -o shop-api.html
```

## Mock servers

A mock server answers requests with your saved **examples**, so a frontend can be built before
the API exists:

1. Save examples on requests: **Save as example** on a response, or write them on the
   request's **Examples** tab.
2. Open the collection's settings, go to **Mock server** and press **Start mock server**. Its
   address, its routes and a live log of requests appear there.

Each request with examples becomes a route (its method and path; `{{var}}`, `:var` and `{var}`
path segments match anything). The first 2xx example answers by default. A caller can pick
another with the `X-Mock-Example: <name>` or `X-Mock-Status: <code>` header. An optional delay
simulates a slow API. Examples you save while it runs are picked up within a second.

```sh
pyro mock shop --port 8090
curl -H 'X-Mock-Status: 404' localhost:8090/v1/orders/42
```
