# Star Wars (Atari, 1983) - native port

Repo: MartinLawes/starwars-arcade-recomp

Atari's 1983 Star Wars cockpit/upright as a native Go + Ebitengine program, same pipeline as Zoo Keeper:
the whole board ported to Go (two 6809s, AVG vector generator, mathbox, 4x POKEY, TMS5220 speech, 6532 RIOT,
ADC0809 yoke, X2212 NVRAM). The original ROMs run unmodified on top of it and are loaded at runtime, never included.

This is the first **vector** game in the pipeline. The video side is NOT a pixel framebuffer - see
[docs/VECTOR-RENDERING.md](docs/VECTOR-RENDERING.md).

## ROMs (required, not included)

Your own `starwars.zip` (MAME `starwars`, revision 2). Files are matched by **CRC**, not name, so MAME names,
dotted names (`136021.214`) or anything else work. Revision 1 parts (`136021-114`, `-103`) are accepted too.

The four mathbox PROMs (110-113) are 1K x 4 chips. Some dumps - including the one this was prepped on - have
floating junk in the high nibble; the loader masks it, after which they match MAME's CRCs exactly.

Verified set (all 13 present and good):

| MAME name | Size | CRC | Role |
|---|---|---|---|
| 136021-214.1f | 16K | 04f1876e | main $6000-$7fff, two banked pages |
| 136021-102.1hj | 8K | f725e344 | main $8000 |
| 136021-203.1jk | 8K | f6da0a00 | main $a000 |
| 136021-104.1kl | 8K | 7e406703 | main $c000 |
| 136021-206.1m | 8K | c7e51237 | main $e000 (vectors) |
| 136021-105.1l | 4K | 538e7d2f | vector ROM, $3000 |
| 136021-107.1jk | 8K | dbf3aea2 | sound $4000 / $c000 |
| 136021-208.1h | 8K | e38070a8 | sound $6000 / $e000 (vectors) |
| 136021-109.4b | 256 | 82fc3eb2 | AVG state PROM (his file is named 136021-105.1l) |
| 136021-110..113 | 1K each | see assets.go | mathbox microcode |

## Running

Put `starwars.zip` next to `starwars.exe` (or in a `roms` folder, the working directory, your home folder, or
pass its path as the first argument). On macOS put it next to the `.app`, in the bundle's `Contents/Resources`,
in `~/Library/Application Support/starwars/`, or in your home folder. High scores and settings (the game's NVRAM) are saved in your user
config folder under `starwars/`.

## Controls

| Input | Action |
|---|---|
| Mouse | Yoke (captured while playing; the yoke holds where you leave it; speed in the menu) |
| Arrows / WASD | Yoke (springs back to centre) |
| Left click / Z / Space / Ctrl | Fire (also starts a game after a coin) |
| Right click / X / Alt | Fire 2 |
| C, V / middle click | Thumb buttons |
| 5 or 6 | Coin |
| Esc / Tab | Settings menu (pauses the game) |
| I | Invert yoke Y (flight-stick style) |
| F2 | Look: vector monitor / clean lines |
| F3 | FPS overlay |
| F11 / Alt+Enter | Full screen |
| F1 (hold) | Test switch (service menu) |

**Gamepad:** left stick or D-pad = yoke, A / right trigger = fire, B / left trigger = fire 2, X / Y = thumb
buttons, Start = fire/start, Select = coin, right bumper = settings menu, hold Select + Start for a second to quit.

## Settings menu

Esc, Tab or the right bumper opens it; the game pauses. Arrows / D-pad move and change values, Enter / A
selects, Esc / B closes. The menu is drawn by the vector renderer in a built-in stroke font.

| Setting | Range | What it does |
|---|---|---|
| Look | Vector monitor / Clean lines / Custom | Presets for the settings below |
| Brightness | 10-200% | Beam energy |
| Glow | Off-200% | Bloom halo around lines |
| Phosphor trails | Off-300% | Persistence: how long moving objects leave a trail |
| Beam dwell | Off-200% | Extra brightness at line ends and dots |
| Beam width | 50-200% | Thickness of the beam |
| Volume | Off-200% | Master volume |
| Speech | Off-200% | TMS5220 voice (movie quotes) |
| Effects and music | Off-200% | The four POKEYs |
| Invert Y | On / Off | Flight-stick style yoke |
| Full screen | On / Off | |

Saved to `settings.json` in the user config folder (`%AppData%\starwars` on Windows, `~/.config/starwars` on Linux).

## Status

- Main board runs: 6809, mathbox (matrix processor, divider, real 23-bit LFSR PRNG), AVG, NVRAM, inputs.
- Attract mode renders correctly headless: high score table, 3D STAR WARS logo, opening crawl, starfield.
- Window + GPU vector renderer (internal/vecrender/gpu), mouse / keyboard / gamepad yoke, NVRAM saving.
- Sound board: second 6809, 6532 RIOT, 4 x POKEY, TMS5220 speech (ports of MAME's devices), lockstep with the
  main CPU, mixed to 48 kHz. Checked against MAME's audio output by spectrogram and level.
- `STARWARS_NOAUDIO=1` runs without an audio device.

## Tools so far

```
go run ./cmd/shot  path/to/starwars.zip out 12 28      # headless vector-rendered PNGs at those seconds (-w -h -clean)
go run ./cmd/boot  path/to/starwars.zip 600            # boot probe on stubbed devices, logs I/O traffic
go run ./cmd/dis   path/to/starwars.zip main 8000 10000 # disassembler (main|sound, start, end, [bank 0|1])
```

`listings/` (gitignored - it is ROM-derived) holds linear disassemblies of main $8000-$ffff, both $6000 bank
pages, and sound $4000-$7fff.

## Releases

Publishing a GitHub release runs `.github/workflows/release.yml`: `starwars.exe` (Windows), `StarWars-linux.tar.gz`
(Linux x86_64, Steam Deck / SteamOS) and `StarWars-mac.zip`. Actions > release > Run workflow gives the same builds as artifacts.

## Layout

| Path | Purpose |
|---|---|
| `internal/m6809` | 6809 core + disassembler, carried over from Zoo Keeper |
| `internal/assets` | CRC-matched ROM loader |
| `internal/mathbox` | Matrix processor, divider, PRNG |
| `internal/avg` | Vector generator - outputs beam strokes, not pixels |
| `internal/board` | Main board wiring |
| `internal/vecrender` | Vector monitor renderer (soft.go = CPU reference / spec for the GPU shaders) |
| `internal/vecrender/gpu` | Real-time Ebitengine renderer (Kage shaders) |
| `internal/sound` | Sound board: CPU, latches, mixer |
| `internal/riot` | 6532 RIOT |
| `internal/pokey` | POKEY |
| `internal/tms5220` | TMS5220 speech synthesizer |
| `cmd/wav` | Headless audio capture to WAV (`SPEECH_ONLY=1` for the voice alone) |
| `cmd/starwars` | The game |
| `cmd/shot` | Headless PNG snapshots |
| `cmd/boot` | Boot probe with stubbed hardware |
| `cmd/dis` | Disassembler |
| `docs/HARDWARE.md` | Memory maps, clocks, interrupts, device list, build order |
| `docs/VECTOR-RENDERING.md` | How the vector display will be drawn |
