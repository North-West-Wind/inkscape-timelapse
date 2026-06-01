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

### Record

```
[ts:8]     uint64 LE     Unix epoch milliseconds
[dlen:4]   uint32 LE     diff byte length
[diff]     dlen bytes    unified diff (uncompressed)
```

Empty diff = document unchanged at that timestamp (produced by
`--record-unchanged`). The first record's diff is from an empty SVG
to the initial document state — effectively the full initial SVG.

## Disclaimer

This project WAS created with assistance from AI (OpenCode).
It served as my first experience with vibe-coding, and [I have written a blog post about it.](https://blog.northwestw.in/p/2026/05/30/my-first-vibe-coding-experience)

I (the human) have taken over control after commit `6d9bb49e147d4faa0e9c0c99af9484e7e612d017`
