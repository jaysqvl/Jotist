# Jotist CLI controls

The compatible executable name remains `scriberr`; login, `~/.scriberr.yaml`,
`SCRIBERR_*`, and existing watcher services keep working. Download a fresh CLI
from the updated server's Settings page to obtain the commands below.

```sh
scriberr login --server http://192.168.10.253:8084
scriberr models
scriberr profiles list
scriberr jobs list
scriberr jobs status JOB_ID
scriberr runs list JOB_ID
scriberr runs recovery JOB_ID RUN_ID
scriberr runs logs JOB_ID RUN_ID
scriberr runs transcript JOB_ID RUN_ID
scriberr runs resume JOB_ID RUN_ID
scriberr jobs cancel JOB_ID
```

Commands print JSON and return a nonzero exit status for server errors. Recovery
responses include stage/attempt state, effective settings, checkpoint availability
and resume eligibility. The server applies the same ownership and compatibility
checks as the web UI. A resumed execution retains its original parameter snapshot;
queue a new run to use newly saved settings or an incompatible updated runtime.

Queue a saved profile with a private JSON request file:

```json
{"profile_id":"PROFILE_ID"}
```

```sh
scriberr queue add JOB_ID --body request.json
scriberr queue list JOB_ID
scriberr queue cancel JOB_ID QUEUE_ITEM_ID
```

For explicit model/device/recovery settings, the request accepts the API's
`parameters` object, for example:

```json
{
  "parameters": {
    "model_family": "cohere_transcribe",
    "model": "CohereLabs/cohere-transcribe-03-2026",
    "device": "cpu",
    "nvidia_precision": "float32",
    "language": "en",
    "no_align": false,
    "diarize": true,
    "diarize_model": "pyannote",
    "diarization_device": "cpu",
    "hf_token_source": "default",
    "recovery_mode": "fixed"
  }
}
```

`profiles create`, `profiles update PROFILE_ID`, `queue order JOB_ID`, and
`settings update` accept API-shaped JSON through `--body FILE` or `--body -`
(stdin). Use a private file or stdin for secrets; do not place a token in shell
arguments. Settings updates omit unchanged values; `hf_token` sets the saved
default, and an empty string clears it. Reading settings never reveals the token.

Profile learning controls are available as `profiles policy`, `reset-adaptive`,
`freeze-adaptive`, and `restore-revision`, with a profile ID. Write commands accept
the corresponding API JSON body, including revision checks. All remaining routes
are accessible with `scriberr api METHOD /api/v1/PATH --body FILE`; requests and
credentials stay on the configured server and redirects are rejected.

These commands operate the server's local workers. The model catalog also retains
the optional OpenAI cloud provider; choose a local model family to keep inference
on your server.
