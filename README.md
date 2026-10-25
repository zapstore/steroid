# steroid

Steroid is the enricher. It writes the about text, the security text, the icon, and the search vector to `data/artifacts.db`. The catalog relay calls `enrich.Listing` on each admitted release, just before it lists it. A cache hit does nothing. An operator can re-run it with `enrich` on `relay.sock` or `relay bundle <app-id>`.

```
steroid -v
steroid [--debug] [--force] app_id
steroid --images-only [--debug] [app_id]
```

The app id selects listings whose id contains it. Omit it with `--images-only` to fetch every listing. Steroid writes about, security, the icon, and the search vector. `--debug` logs each step and writes `prompt` and `response.json` under `data/debug/<app-id>/`. `--force` deletes that app's row first, so the cache is not reused. `--images-only` fetches the listing icon and the author's picture and skips the model. The server writes the snapshot.

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

`-as dev` or `-as test` drafts new dev or test cases instead of train. They are model-labeled like any draft, so they go to `evals/about-review.jsonl`, and only count once a person has checked them and moved them to `evals/about.jsonl`.

## Reviewing drafts

```
make review
```

This opens a local page at `http://127.0.0.1:8787` that walks through every pending case in `evals/about-review.jsonl` and `evals/security-review.jsonl`. It shows the evidence next to the labels, and checks each edit as you type, with the same checks the eval runs. Approve moves the case, as edited, to `evals/about.jsonl` or `evals/security.jsonl`, drops its `draft` tag, and tags it `reviewed`. Reject moves it to `evals/<eval>-rejected.jsonl` with your reason in its note. Both can be undone. Edits to a case you have not decided on yet are saved in the review file. Press `?` on the page for the keyboard shortcuts.

## Security evals

`evals/security.jsonl` holds labeled APKs: the scan rows, the source digest, and the listing, exactly as the enricher sends the security call. `securityeval` works like `abouteval`.

```
make eval-security                                   # dev split, LLM_MODEL
make eval-security ARGS="-split test -n 3 -model my-finetune"
make eval-security ARGS="-split all -show"           # no model: print the cases for review
make eval-security ARGS="-export sft-security.jsonl" # train split as chat fine-tuning examples
make eval-security ARGS="-mine data/snapshots/1.tar.zst -limit 300 -j 6" > mined.jsonl
make eval-security ARGS="-draft mined.jsonl -as dev -model deepseek/deepseek-v4-pro" > evals/security-review.jsonl
```

`-mine` scans each listing's APK and reads its repository under `DATA_DIR`, as the enricher does, without calling the model. Listings are taken in a fixed order spread across the snapshot, and ids already in `-cases` are skipped. Each case is tagged by what it exercises: `tracker`, `ads`, `google`, `offline`, `location`, `camera-mic`, `system-access`, `packages`, `no-access`, `no-source`, and others.

A case's `want`:

- `warning`: whether the note opens with one warning sentence and a blank line.
- `must`, `must_not`: phrase groups for the note, as in the about eval.
- `notes`: phrase groups each fact's note contains, such as `{"camera": [["qr", "barcode"]]}`.
- `action`: `keep` or `write`, only for a case with `current`, the stored note.
- `security`, `facts`: the reference reply. Train cases need it to be exported.

Every reply is also run through the enricher's own check, then checked against the prompt rules: under 500 characters, English, no hedges ("may", "could"), nothing about what the app lacks, no permission ids, safe, scan, or source in the note, a host only when the source shows it, one warning sentence at most. The fact rows must match the scan, one per fact with its value and nothing added. Each permission note starts with its permission id, and a tracking or ads note names each library. `missed` is how often a note that needs a warning had none. `extra` is how often one opened with a warning it should not have.

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
