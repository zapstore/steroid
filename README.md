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

## About evals

`evals/about.jsonl` holds labeled listings. `abouteval` sends each one the about call the enricher would send, scores the reply as it arrived (no retry, no fallback model), and prints pass rates.

```
make eval                                  # dev split, LLM_MODEL
make eval ARGS="-split all -n 3 -out r.jsonl"
make eval ARGS="-model my-finetune -split test"
make eval ARGS="-split all -candidates"    # no model: check the labels and the offered descriptions
make eval ARGS="-export sft.jsonl"         # train split as chat fine-tuning examples
make eval ARGS="-mine events.jsonl" > new.jsonl   # unlabeled cases from a snapshot, tagged
make eval ARGS="-draft new.jsonl -model deepseek/deepseek-v4-pro" > evals/about-draft.jsonl
make eval ARGS="-cases evals/about.jsonl,evals/about-draft.jsonl -export sft.jsonl"
```

`-draft` has a model label unlabeled cases as train drafts. A draft whose reference reply fails its own labels is sent back with the reason, up to three times, then dropped. Ids already in `-cases` are skipped, so dev and test never become train. `evals/about-draft.jsonl` is model-labeled and unreviewed: move a case to `evals/about.jsonl` once a person has checked it. `deepseek-v4-pro` agreed best with the reviewed labels (28 of 35 keep-or-write calls).

A case has a `split` (`train`, `dev`, or `test`) and a `want`:

- `action`: `keep` when the offered description is the right about, `write` when it is not, absent when either is fine.
- `must`: groups of phrases. The about contains one phrase of every group. Phrases are lowercase substrings, so `shak` matches "shake" and "shaking".
- `must_not`: phrases that never appear, such as the hook the description opens with.
- `about`, `keywords`, `queries`: the reference reply. Train cases need it to be exported.

Besides `must` and `must_not`, every about is checked for length (100 to 300), the enricher's own rejections, English, the app name, and garbled edits ("the the app", a sentence that starts lowercase). `trap` is how often the model kept a description that fails its case. `rewrite` is how often it wrote over one labeled `keep`.

Only `train` is exported. Tune prompts and checks against `dev`. Run `test` to compare models, and do not label from its failures.

## Environment

Reads `.env` from the working directory. A variable already set in the process wins. Every variable is in [`.env.example`](.env.example).

`DATA_DIR` is required: the relay's data directory. Listings are read from `DATA_DIR/relay.db`. Artifacts are written to `DATA_DIR/artifacts.db`. `--debug` writes under `DATA_DIR/debug/<app-id>/`.

Remote model, all `LLM_`. `LLM_URL` is the API root. `LLM_MODEL` is required: first entry, then comma-separated fallbacks on that provider. `LLM_FALLBACK_URL` and `LLM_FALLBACK_API_KEY` are both set or both absent.

| Variable | Required |
|---|---|
| `LLM_URL`, `LLM_API_KEY`, `LLM_MODEL` | yes |
| `LLM_FALLBACK_URL`, `LLM_FALLBACK_API_KEY` | together, or neither |

Local vector model, separate from `LLM_*`.

| Variable | Default |
|---|---|
| `LEAF_MODEL_DIR` | `DATA_DIR/models/leaf-ir-v1`, downloaded on first use |
| `ONNXRUNTIME_LIB` | downloaded next to the model |

An APK is read from the relay's blob, `DATA_DIR/blossom/blobs/<sha256>.apk`, when it is there: in the buffer zone, or always when the relay runs without Bunny. Otherwise it is fetched from `BLOSSOM_URL/<sha256>`, then from the asset URL.

| Variable | Default |
|---|---|
| `BLOSSOM_URL` | `https://cdn.zapstore.dev` |
