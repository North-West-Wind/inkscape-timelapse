# inkscape-timelapse

Inkscape extension for creating timelapses. Extension currently a template stub.

## Toolchain

- **Package manager**: `uv` (not pip/poetry)
- **Python**: >=3.12 (per `.python-version`)
- **Dependency**: `inkex >= 1.4.1` (Inkscape extension framework)
- **License**: GPL v3

## Commands

```sh
uv run timelapse.py       # run the extension standalone (for testing)
uv run python timelapse_tests.py  # run tests (unittest-based, inkex.tester)
```

There is no pytest config — tests use `inkex.tester.TestCase` (unittest compat).

## Architecture

- **`timelapse.py`** — entrypoint; defines `TimelapseExtension(inkex.EffectExtension)`. Must be invoked by Inkscape (or standalone for debugging).
- **`timelapse.inx`** — Inkscape extension manifest. Registers as menu: Extensions → Timelapse → *`. Script entry in INX references `inkscape_timelapse.py` (must match the installed filename).
- **`main.py`** — placeholder, not the real entrypoint.
- **`timelapse_tests.py`** — test stub with broken imports (`my_effect_extension`, `UnnamedEffectExtension`). Needs updating to match current code before tests will pass.

## Installation

Copy `timelapse.py` and `timelapse.inx` to the Inkscape extensions directory (e.g. `~/.config/inkscape/extensions/`).

## Testing quirks

- `timelapse_tests.py` currently has stale imports — an agent should fix these before trying to run tests.
- Inkex test framework (`inkex.tester`) uses `unittest` under the hood; no pytest plugins needed.
