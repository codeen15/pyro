# API docs, mock servers and collection settings

## Collection and folder settings
The settings button on a collection or folder row (sliders icon) edits what its requests
inherit, saved to `collection.yaml` / `folder.yaml`: name, description (Markdown, used in the
generated docs), variables (with secret masking), auth and pre-request/test scripts. The
`+` in the Collections header creates an empty collection. API: `GET/PUT
/api/v1/collections/{slug}`, `GET/PUT /api/v1/folders/{path}`.

## Generated API docs
```sh
pyro docs shop -o shop-api.html
```
or the book icon on a collection row (opens in a new tab), or
`GET /api/v1/collections/{slug}/docs` (`?download=1` to save).

One self-contained HTML page (no external assets), light or dark following the reader's
system, with a filterable sidebar. For every request: method and URL (variables highlighted),
description (Markdown, raw HTML dropped so imported text can't inject script), the auth it
uses and where that comes from, query parameters and headers (optional ones marked), body,
saved examples, and a cURL command using the request's effective auth.

**What it never shows:** secret variable values (masked) and auth credentials. Auth is
described by type and the variables it uses (`Bearer token using {{token}}`); in cURL,
credentials that aren't variable references become `<username>`-style placeholders.

## Mock servers
```sh
pyro mock shop --port 8090 [--delay 200ms]
curl localhost:8090/v1/orders/42
curl -H 'X-Mock-Status: 404' localhost:8090/v1/orders/42
```
or the **Mock server** tab in a collection's settings (start, stop, routes, recent
requests), or `POST/GET/DELETE /api/v1/mocks`.

- **Routes:** every HTTP request with saved examples. The path comes from its URL: the host
  is dropped, and a leading `{{baseUrl}}` contributes its own path (so
  `baseUrl = https://api.test/v1` serves `/v1/...`). `{{id}}`, `:id` and `{id}` segments match
  any value; when several routes match, the one with the most literal segments wins. `HEAD`
  falls back to `GET`.
- **Which example answers:** `X-Mock-Example: <name>` (or `?__example=`), else
  `X-Mock-Status: <code>` (or `?__status=`), else the first 2xx example, else the first one.
  The response carries the example's status, headers and body, plus `X-Pyro-Mock-Example`.
- **Live:** routes are re-read from the workspace at most once a second, so examples saved
  while the mock runs are served right away.
- **No match:** `404` with the list of routes. CORS is open (`Access-Control-Allow-Origin: *`,
  preflights answered) so browser apps can call the mock.
- Mocks started from the UI listen on `127.0.0.1` (any free port unless given); `pyro mock`
  binds `127.0.0.1:8090` by default (`--host` to change).

Examples come from **Save as example** on responses, Postman examples, and OpenAPI response
examples, so an imported OpenAPI file becomes a working mock in one step:
```sh
pyro import openapi.yaml && pyro mock <collection>
```
