# Voice Timeline Renderer

Voice Timeline Renderer is a self-hosted Go service for turning validated PCM
WAV recordings into a crossfaded, peak-normalized mixdown. The service exposes
an idempotent HTTP surface, keeps media and metadata under one data directory,
and records every render transition.

The implementation covers:

- 16-bit integer PCM WAV validation and signal measurement.
- Ordered timeline spans with source-frame selection, gain, and crossfade.
- Linear sample-rate conversion and mono/stereo channel mapping.
- Equal-power neighboring-span crossfades.
- Peak normalization into a requested dBFS target.
- Durable render states, receipts, and atomically committed output folders.

Automated tests are intentionally deferred. A later task stage will add unit,
integration, and end-to-end verification around the production interfaces.

## Requirements

- Go 1.23 or newer.
- A writable local directory for source media, timeline metadata, render jobs,
  output WAV files, and receipts.

No network service, database server, or third-party Go module is required.

## Build

```bash
go build ./...
```

## Run

```bash
go run ./cmd/voiceservice \
  -addr :8093 \
  -data ./var/voice-timeline
```

The health endpoint is `GET http://127.0.0.1:8093/healthz`.

Runtime settings are supplied through arguments:

- `-addr`: HTTP listen address, default `:8093`.
- `-data`: persistent data root, default `./var/voice-timeline`.

No environment variable is required. Do not put credentials in the repository;
the service does not consume any remote credential.

## HTTP interface

### Register a source

`PUT /v1/sources/{source_id}`

```json
{
  "name": "opening take",
  "wav_base64": "UklGR..."
}
```

The source ID is chosen by the caller and must be 3-64 characters using
letters, digits, underscore, or hyphen. Repeating the same ID with exactly the
same name and bytes is idempotent. Reusing it with different content returns
the `source_conflict` error.

Response:

```json
{
  "id": "opening_take_01",
  "name": "opening take",
  "digest": "b7c0...",
  "byte_count": 96044,
  "sample_rate": 48000,
  "channels": 1,
  "bits_per_sample": 16,
  "frame_count": 48000,
  "duration_ms": 1000,
  "peak_dbfs": -8.0,
  "rms_dbfs": -11.0,
  "created_at": "2026-11-09T12:00:00Z"
}
```

### Compose a timeline

`PUT /v1/timelines/{timeline_id}`

```json
{
  "name": "two part narration",
  "sample_rate": 48000,
  "output_channels": 2,
  "target_peak_dbfs": -1,
  "spans": [
    {
      "source_id": "opening_take_01",
      "source_start_frame": 0,
      "source_end_frame": 48000,
      "gain_db": -2,
      "crossfade_frames": 0
    },
    {
      "source_id": "closing_take_01",
      "source_start_frame": 0,
      "source_end_frame": 44100,
      "gain_db": 0,
      "crossfade_frames": 4800
    }
  ]
}
```

Spans are sequential. A later span overlaps its predecessor by
`crossfade_frames`; the overlap is mixed with equal-power weights. A crossfade
must fit within both neighboring spans.

### Start a render

`PUT /v1/renders/{render_id}`

```json
{
  "timeline_id": "two_part_narration_01"
}
```

The render runs through the durable states
`queued -> rendering -> finalizing -> completed`. A failed render records a
terminal `failed` state with a stable error code and does not expose a partial
output folder.

### Read resources

- `GET /v1/sources/{source_id}`
- `GET /v1/timelines/{timeline_id}`
- `GET /v1/renders/{render_id}`
- `GET /v1/renders/{render_id}/receipt`
- `GET /v1/renders/{render_id}/output`

The output route returns `audio/wav`. The receipt records source digests, span
placement, channel mapping, measured peak and RMS amplitude, normalization
gain, output digest, and non-fatal warnings.

### Compare two renders

`GET /v1/renders/{render_id}/diff/{other_render_id}`

The diff is a strictly read-only comparison of two completed renders. It never
transitions a job, writes metadata, or touches an output file, so comparing
two renders leaves both unchanged and repeated calls return the same report.

For two outputs that share a PCM format, the response reports whether the
audio is `identical` or `different`, and when different it locates the first
diverging frame and channel together with both signed 16-bit PCM values, or a
`length` boundary when one output ends first:

```json
{
  "status": "different",
  "comparable": true,
  "audio_identical": false,
  "left": {
    "render_id": "render_a",
    "timeline_id": "timeline_01",
    "state": "completed",
    "schema_version": 1,
    "sample_rate": 48000,
    "channels": 2,
    "bits_per_sample": 16,
    "frame_count": 19200,
    "byte_count": 76844,
    "digest": "aef6..."
  },
  "right": {
    "render_id": "render_b",
    "timeline_id": "timeline_02",
    "state": "completed",
    "schema_version": 1,
    "sample_rate": 48000,
    "channels": 2,
    "bits_per_sample": 16,
    "frame_count": 19200,
    "byte_count": 76844,
    "digest": "9db9..."
  },
  "version_match": true,
  "compared_frames": 19200,
  "first_difference": {
    "kind": "sample_value",
    "frame_index": 1,
    "channel": 0,
    "sample_index": 2,
    "left_value": 642,
    "right_value": 808
  }
}
```

A different sample rate, channel count, or sample width is a formatting fact,
not an audio difference. Such pairs return `status: "not_comparable"` with a
stable `reason_code` of `render_format_mismatch` and the exact mismatching
facts in `format_mismatches`, rather than a misleading sample-level verdict.
The same `not_comparable` status, each with its own reason code, is returned
for renders that are not finished (`render_not_completed`), that failed
(`render_failed`), whose output artifacts have been cleaned from disk
(`render_artifacts_missing`), whose output no longer matches its receipt digest
(`render_output_integrity_error`), or that cannot be decoded
(`render_output_unreadable`). Unknown render identifiers still return the
standard `404` error envelope.


## Data directory

```text
var/voice-timeline/
  sources/<source_id>/
    source.wav
    metadata.json
  timelines/<timeline_id>.json
  jobs/<render_id>.json
  renders/<render_id>/
    output.wav
    receipt.json
  tmp/
```

Source and render folders are assembled in `tmp/` and moved into place with one
directory rename. Metadata updates use same-directory temporary files followed
by rename.

## Source layout

```text
cmd/voiceservice/       service entry point
cmd/timelinecheck/      workflow execution entry point
internal/httpapi/       routing, request limits, JSON errors, media response
internal/service/       workflow orchestration and idempotency
internal/domain/        entities, validation, errors, and render transitions
internal/audiocodec/    WAV handling, transforms, and signal measurement
internal/render/        timeline preparation, mixing, normalization, receipt
internal/storage/       atomic local repository
internal/checkflow/     bounded production workflow checks
```

## Workflow checks

Run each declared production check from the repository root:

```bash
go run ./cmd/timelinecheck -workflow ingest-audio-source
go run ./cmd/timelinecheck -workflow compose-timeline
go run ./cmd/timelinecheck -workflow render-mixdown
go run ./cmd/timelinecheck -workflow compare-renders
```

Each check creates an isolated temporary data directory, starts the real HTTP
handler, sends generated WAV inputs, and verifies read-back or rendered output.
The checks are bounded and do not require network access.

## Deferred test boundary

This baseline contains no unit tests, test fixtures, browser checks, or
`test_command`. The downstream task stage owns test creation and red/green
verification. It should extend coverage around WAV chunk edge cases, timeline
crossfade bounds, failed job cleanup, concurrent idempotency, and HTTP error
mapping.
