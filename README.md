# inkscape-timelapse

Capture timelapses from a running Inkscape instance via D-Bus. Records
incremental SVG diffs to an append-only binary file. Render to .webm
video with transparency.

Single Go binary, no runtime dependencies.

## Usage

### Record

```
inkscape-timelapse record --interval=5 --save-dir=./recordings
```

Records the currently focused Inkscape document at regular intervals.
Captures follow window focus — injects a `data-tlid` marker into all
elements at startup to verify window identity before each capture.

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | `5` | Seconds between captures |
| `--save-dir` | current dir | Recording output directory |
| `--append` | — | Continue recording to an existing .tld file |
| `--record-unchanged` | off | Record empty diffs when window unfocused |
| `--time-format` | `2006-01-02T15:04:05Z` | Go time format for capture timestamps |

To continue a previous recording:
```
inkscape-timelapse record --append recording.tld --interval=5
```

All state is in-memory. If the process crashes, the incomplete recording
file is still renderable (truncated records at EOF are skipped).

### Render

```
inkscape-timelapse render recording.tld --framerate=10 --output=timelapse.webm
```

Reconstructs each SVG frame by applying incremental diffs, renders to PNG
in parallel via `rsvg-convert` (default) or `inkscape --pipe`, and pipes
directly to `ffmpeg`.

| Flag | Default | Description |
|------|---------|-------------|
| `--framerate` | `10` | Output video frame rate |
| `--output` | `timelapse.webm` | Output video path |
| `--skip-unchanged` | off | Skip frames where document didn't change |
| `--resolution-policy` | `max` | Video sizing: `max`, `min`, `initial` |
| `--resolution` | — | Explicit WxH (overrides --resolution-policy) |
| `--align-x` | `center` | Horizontal alignment: `left`, `center`, `right` |
| `--align-y` | `center` | Vertical alignment: `top`, `center`, `bottom` |
| `--stretch` | off | Stretch SVG to fill canvas (overrides alignment) |
| `--engine` | `rsvg` | Render engine: `rsvg` (fast) or `inkscape` |
| `--workers` | CPU count | Parallel render workers |

Output codec: libvpx-vp9 with yuva420p (transparency).

### Inspect / Decode

```
inkscape-timelapse jsonl recording.tld     # JSONL to stdout
inkscape-timelapse analyze recording.tld    # Frame count and resolution stats
inkscape-timelapse extract recording.tld --frame=42 --output=frame.svg
```

## .tld format

Binary, append-only, no footer. Each record is independently deflated
and length-prefixed, so truncated trailing data is safely ignored.

```
TLTL                     magic

[len:4] [deflated record]  ...
```

### Initial SVG (type 0x00)

```
[00]    1 byte     record type
[slen]  4 bytes    SVG byte length (LE u32)
[svg]   slen bytes UTF-8 encoded SVG
```

### Capture (type 0x01)

```
[01]      1 byte     record type
[tlen]    4 bytes    timestamp byte length (LE u32)
[ts]      tlen bytes UTF-8 timestamp
[dlen]    4 bytes    diff byte length (LE u32)
[diff]    dlen bytes unified diff (uncompressed)
```

Empty diff = document unchanged at that timestamp (produced by
`--record-unchanged`).

## Disclaimer

This project was created with assistance from AI (OpenCode).
