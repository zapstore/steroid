# steroid

Enriches Zapstore listings and publishes catalog snapshots from the relay database.

```
steroid -v
steroid enrich --filter app_id [--debug] [--force]
steroid serve
steroid bundle [--filter app_id] [--no-enrich] [--debug]
```

`enrich` writes about, security, facts, icon, and embedding for listings whose app ID contains the filter. `--debug` writes `prompt` and `response` under `data/debug/<app-id>/`. `--force` deletes those app directories first, so the cache is not reused.

`bundle` reads the relay database, enriches changed listings, and publishes the next snapshot. The snapshot is the full catalog. `--filter` limits enrichment and the diff to those apps. `--no-enrich` publishes without enriching.

`serve` answers `GET /deltas?from=<epoch>` (default `localhost:3339`). Set `BUNDLE_INTERVAL` to a Go duration to publish a bundle on that interval while serving.

## Build

```
make build
```

Needs CGO (`go-sqlite3` with `fts5`). The binary is `dist/steroid`.

## Environment

Reads `.env` from the working directory. Variables already set in the process win.

| Variable | Role |
|---|---|
| `DATA_DIR` | Artifacts and snapshots (default `data`) |
| `RELAY_DB` | Relay SQLite path |
| `SYSTEM_DIRECTORY_PATH` | If `RELAY_DB` is unset, uses `<path>/data/relay.db` |
| `EMBED_PROVIDER_URL`, `EMBED_API_KEY` | Remote LLM for overviews |
| `EMBED_MODEL` | Model, then fallbacks, comma-separated |
| `LEAF_MODEL_DIR` | Local embedding model (default `.tools/leaf-ir-v1`) |
| `SIGN_WITH` | Key for `serve` and `bundle`: `1`, hex, `nsec`, or a NIP-46 bunker URL |
| `STEROID_ADDRESS` | Listen address (default `localhost:3339`) |
| `BUNDLE_INTERVAL` | Optional bundle period while serving |
