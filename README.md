# steroid

Steroid is the enricher. It writes the about text, the security text, the icon, and the search vector to `data/artifacts.db`. The catalog relay calls `github.com/zapstore/steroid/enrich` on each bundle tick for the apps in the diff. A cache hit does nothing. The relay then writes the snapshot.

```
steroid -v
steroid [--debug] [--force] app_id
```

The app id selects listings whose id contains it. Steroid writes about, security, the icon, and the search vector. `--debug` logs each step and writes `prompt` and `response.json` under `data/debug/<app-id>/`. `--force` deletes that app's row first, so the cache is not reused. The server writes the snapshot.

## Build

```
make
make linux-arm64
make linux-amd64
```

Needs CGO (`go-sqlite3`). The binary is `dist/steroid-<os>-<arch>`.

## Environment

Reads `.env` from the working directory. A variable already set in the process wins.

`SYSTEM_DIRECTORY_PATH` defaults to `.`. Listings are read from `<root>/data/relay.db`. Artifacts are written to `<root>/data/artifacts.db`. `--debug` writes under `<root>/data/debug/<app-id>/`.

Remote model, all `LLM_`. `LLM_URL` is the API root. `LLM_MODEL` is required: first entry, then comma-separated fallbacks on that provider. `LLM_FALLBACK_URL` and `LLM_FALLBACK_API_KEY` are both set or both absent.

| Variable | Required |
|---|---|
| `LLM_URL`, `LLM_API_KEY`, `LLM_MODEL` | yes |
| `LLM_FALLBACK_URL`, `LLM_FALLBACK_API_KEY` | together, or neither |

Local vector model, separate from `LLM_*`.

| Variable | Default |
|---|---|
| `LEAF_MODEL_DIR` | `.tools/leaf-ir-v1` |
| `ONNXRUNTIME_LIB` | the library under `.tools` |

Shared with `zsp`. Same names, same meaning.

| Variable | Default |
|---|---|
| `APK_CACHE` | unset: do not read or write the cache |
| `BLOSSOM_URL` | `https://cdn.zapstore.dev` |
