# 0004 — One YAML file per request, single `.req.yaml` extension

**Status:** Accepted (2026-11-01)

## Context
Postman stores a whole collection in one large JSON file, which diffs and merges badly.
Pyro collections should live in git next to the code.

## Decision
- A workspace is a directory tree. Folders are directories, and each request is one YAML file.
- **Every request kind uses `.req.yaml`.** The `kind` field (`http`, `websocket`, `socketio`, `plugin:<name>`) picks the driver. An earlier draft used `.ws.yaml` for streams; it was dropped so a request can change kind without being renamed, and so tooling has a single pattern to match.
- Requests are addressed by their path relative to `collections/` without the extension.

## Consequences
- Small, reviewable diffs, and editing by hand or in any editor works.
- Renaming a file changes the request's identity (path). Load plans that reference it must be updated; a rename in the UI will rewrite references.
- Writes are atomic (temp file + rename) so external tools never see partial files.
