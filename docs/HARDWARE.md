# Star Wars hardware notes

Reference: MAME `src/mame/atari/starwars.cpp`, `starwars_m.cpp`, `src/devices/video/avgdvg.cpp`.

## Clocks

- Master crystal 12.096 MHz
- Main 6809E and sound 6809E: 12.096 / 8 = 1.512 MHz
- IRQ (main): 12.096 MHz / 4096 / 12 = ~246 Hz = every 6144 CPU cycles. Acked by any write to $4660-$467f.
- Vector refresh as MAME models it: IRQ / 6 = ~41 Hz. The real monitor redraws whenever the AVG finishes a list,
  so frame pacing comes from the game, not a fixed vblank.
- Watchdog: 12.096 MHz / 4096 / 128 = ~23 Hz. Kicked via $4640.
- POKEY x4: 1.512 MHz. TMS5220: 12.096 / 2 / 9 = 672 kHz. ADC0809: 756 kHz.

## Main CPU map

| Address | R/W | What |
|---|---|---|
| $0000-$2fff | RW | Vector RAM (AVG display lists) + work RAM |
| $3000-$3fff | R | Vector ROM (136021-105) - fonts, shapes |
| $4300 | R | IN0: coin2, coin1, service, tilt, test, -, button4, button1 (active low) |
| $4320 | R | IN1: bit 2 diag step, bit 4 button3, bit 5 button2, **bit 6 AVG halted**, **bit 7 mathbox running** |
| $4340 / $4360 | R | DSW0 / DSW1 |
| $4380 | R | ADC result (yoke) |
| $4400 | R/W | main latch (from sound) / sound latch (to sound) |
| $4401 | R | bit 7 sound latch full, bit 6 main latch full |
| $4500-$45ff | RW | X2212 NVRAM (4 bits wide) |
| $4600 | W | AVG go |
| $4620 | W | AVG reset |
| $4640 | W | watchdog |
| $4660 | W | IRQ ack |
| $4680-$4687 | W | LS259 (bit 7 of data): 0/1 coin counters, 2/3/6 LEDs, **4 ROM bank**, 5 PRNG reset, 7 NVRAM recall |
| $46a0 | W | NVRAM store |
| $46c0-$46c3 | W | ADC channel select + start: 0 pitch (Y), 1 yaw (X), 2 thrust (unused) |
| $46e0 | W | sound board reset + clear both latches |
| $4700-$4707 | W | mathbox: registers / divider / run start |
| $4700 / $4701 | R | divider result high / low |
| $4703 | R | PRNG |
| $4800-$4fff | RW | CPU + math RAM |
| $5000-$5fff | RW | math RAM (shared with mathbox) |
| $6000-$7fff | R | banked ROM, LS259 bit 4 picks page |
| $8000-$ffff | R | fixed ROM |

Vectors: RESET $f261, IRQ $ef56, all others $ef54.

## Sound CPU map

| Address | What |
|---|---|
| $0000-$07ff | W main latch (to main CPU) |
| $0800-$0fff | R sound latch (from main) |
| $1000-$107f | 6532 RIOT RAM |
| $1080-$109f | 6532 RIOT I/O + timer. PA0 TMS5220 /WS, PA1 /RS, PA2 /READY, PA6 main latch full, PA7 sound latch full. PB = TMS5220 data |
| $1800-$183f | 4 x POKEY (16 regs each) |
| $2000-$27ff | RAM |
| $4000-$7fff | ROM, mirrored at $c000-$ffff |

Vectors: RESET $7d44, IRQ $7df3 (from the RIOT timer / PA7 edge), others $7e27.

## Boot probe result (stubbed devices, 600 frames)

Clean: no illegal opcodes, ~1 AVG go per frame, bank flips, NVRAM read/written, mathbox exercised
(27K writes to $4700, 14K to $4702, 18K PRNG reads), 13 sound commands sent. The map above is confirmed.
The stubs say "mathbox never busy", so 3D results are garbage until the mathbox exists.

## Devices to write, in order

1. **Mathbox** (`internal/mathbox`): the 2901-style bit-slice microsequencer driven by the four PROMs. MAME's
   `starwars_mproc` + divider + PRNG is the reference. Nothing 3D is right without it. Test: lockstep its RAM
   writes against a reference trace.
2. **AVG** (`internal/avg`): state machine from the 136021-109 PROM, reads display lists from main RAM/ROM.
   Outputs a list of beam segments (x0,y0,x1,y1, RGB 3-bit, intensity 8-bit x 3-bit DAC) - NOT pixels.
   Halt flag back to IN1 bit 6.
3. **Vector renderer** - see VECTOR-RENDERING.md.
4. **Inputs**: yoke as analog (ADC 0-255, stick or mouse), 4 buttons, coin/start. Controller mapping like the
   other ports: Select = coin, Start = start, hold both to quit.
5. **Sound board**: second 6809, 6532 RIOT (timer IRQ), 4 POKEYs, TMS5220 LPC speech (all the movie quotes come
   from it - this one has to sound right), two latches with handshake.
6. **X2212 NVRAM** saved to user config folder (high scores, settings).
7. Release workflow: Windows, Linux (SteamOS), Mac - copy from Zoo Keeper.

Then the usual extras on top of a working original: widescreen/HUD options, high score persistence.
