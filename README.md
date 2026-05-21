# inkscape-timelapse

Capture timelapses from a running Inkscape instance via D-Bus. Records
incremental SVG diffs to an append-only binary file. Render to .webm
video with transparency.

## Usage

### Capture

```
uv run timelapse_capture.py start --interval=5 --save-dir=./recordings
```

Records the currently focused Inkscape document at regular intervals.
Captures follow window focus — injects a `data-tlid` marker into all
elements at startup to verify window identity before each capture.

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | `5` | Seconds between captures |
| `--save-dir` | current dir | Recording output directory |
| `--record-unchanged` | off | Record empty diffs when window unfocused |
| `--time-format` | `%Y-%m-%dT%H:%M:%SZ` | strftime format for capture timestamps |

```
uv run timelapse_capture.py stop
```

To continue a previous recording:
```
uv run timelapse_capture.py start --append recording.tld --interval=5
```

All state is in-memory. If the process crashes, the incomplete recording
file is still renderable (truncated records at EOF are skipped).

### Render

```
uv run timelapse_render.py recording.tld --framerate=10 --output=timelapse.webm
```

Streams through the .tld file one record at a time (O(1) memory per frame).
Reconstructs each SVG frame by applying incremental diffs, renders to PNG
via `inkscape --pipe`, and pipes directly to `ffmpeg`.

| Flag | Default | Description |
|------|---------|-------------|
| `--framerate` | `10` | Output video frame rate |
| `--output` | `timelapse.webm` | Output video path |
| `--skip-unchanged` | off | Skip frames where document didn't change |
| `--resolution-policy` | `max` | Video sizing: `max`, `min`, `initial` |
| `--align-x` | `center` | Horizontal alignment: `left`, `center`, `right` |
| `--align-y` | `center` | Vertical alignment: `top`, `center`, `bottom` |
| `--stretch` | off | Stretch SVG to fill canvas (overrides alignment) |
| `--engine` | `rsvg` | Render engine: `rsvg` (fast) or `inkscape` |
| `--workers` | CPU count | Parallel render threads |

Output codec: libvpx-vp9 with yuva420p (transparency).

## .tld format

Binary, append-only, no footer. Records past EOF are ignored on read
(crash-safe).

```
MAGIC    4 bytes    "TLTL"
```

Two record types follow the magic:

### Initial SVG (type 0x00)

```
[00]    1 byte     record type
[slen]  4 bytes    SVG byte length (little-endian unsigned 32-bit)
[svg]   slen bytes UTF-8 encoded SVG
```

### Capture (type 0x01)

```
[01]      1 byte     record type
[tlen]    4 bytes    timestamp byte length (LE u32)
[ts]      tlen bytes UTF-8 timestamp
[dlen]    4 bytes    diff byte length (LE u32)
[diff]    dlen bytes zlib-compressed unified diff
```

Empty diff = document unchanged at that timestamp (produced by
`--record-unchanged`).

## Disclaimer

This project was created with assistance from AI (OpenCode).
