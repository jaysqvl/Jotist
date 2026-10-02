# Jotist CLI controls

The executable is `jotist`. Install or update it from the server's Settings page.

Configuration is saved to `~/.jotist.yaml`. `--config FILE` selects another
file explicitly.
`JOTIST_SERVER_URL`, `JOTIST_TOKEN`, and `JOTIST_WATCH_FOLDER` override saved
configuration. The background service is `jotist-watcher`; logs are written to
`jotist-service.log` in the system temporary directory. Upload session state is
stored under `jotist` in the user's configuration directory. When upgrading an
existing installation, follow the [migration guide](jotist-migration.md).

```sh
jotist login --server http://192.168.10.253:8084
jotist models
jotist profiles list
jotist jobs list
jotist jobs status JOB_ID
jotist runs list JOB_ID
jotist runs recovery JOB_ID RUN_ID
jotist runs logs JOB_ID RUN_ID
jotist runs transcript JOB_ID RUN_ID
jotist runs resume JOB_ID RUN_ID
jotist jobs cancel JOB_ID
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
jotist queue add JOB_ID --body request.json
jotist queue list JOB_ID
jotist queue cancel JOB_ID QUEUE_ITEM_ID
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
are accessible with `jotist api METHOD /api/v1/PATH --body FILE`; requests and
credentials stay on the configured server and redirects are rejected.

These commands operate the server's local workers. The model catalog also retains
the optional OpenAI cloud provider; choose a local model family to keep inference
on your server.
