# Vector rendering - making it look like a vector monitor

Rule: **never rasterise to a low-res framebuffer and scale up.** A vector monitor has no pixels. The game's
"resolution" is the DAC precision of the beam, which on Star Wars is far finer than any 1983 raster.

## What the AVG gives us per segment

- Beam start and end in DAC coordinates (sub-pixel precise at any window size)
- Colour: 3-bit RGB (Star Wars uses a colour XY monitor)
- Intensity: 8-bit value x 3-bit resistor DAC (12k/24k/47k network) = real brightness levels, not on/off
- Zero-length segments = dots (stars, laser tips) - these get the beam's full dwell and are the brightest
  things on the screen

## Pipeline (Ebitengine, all GPU)

1. **Geometry at native resolution.** Each segment becomes a quad (thick line) built with `DrawTriangles` in
   window pixels, recomputed every frame for the current window size. 4K window = 4K-sharp lines.
2. **Beam profile, not hard edges.** A Kage shader gives each line a Gaussian cross-section: bright core,
   soft falloff. Width scales with intensity (brighter beam = slightly fatter spot, like a real gun).
3. **Additive blending into a float (HDR) buffer.** Where lines overlap or cross, they get brighter, exactly as
   on the tube. Line ends and vertices get extra energy (the beam dwells at endpoints), so corners pop.
4. **Brightness by speed.** Long segments drawn in the same time as short ones are dimmer per unit length; the
   shader scales energy by 1/length within limits. This is a big part of the "real vector" look.
5. **Phosphor persistence.** The previous frame's buffer is faded (per-channel decay, P22-style colour phosphor)
   and added under the new frame, so fast-moving objects (TIE fighters, fireballs, the trench) leave short trails.
6. **Bloom/glow.** Downsample chain + blur of the HDR buffer, added back: the halo around every line.
7. **Tone map** to the display, black stays black (no grey haze), optional slight curvature/glass reflection.

## Settings (on by default, adjustable)

- Beam width, glow amount, persistence, brightness/gamma
- "Clean" mode (sharp lines, no glow) for people who want it

## Hit flash

MAME's known bug: the monitor "overdrive" when you're hit isn't simulated. We can: it's a brightness/blooming
surge over the whole frame - easy to add in the tone-map pass.

## Aspect

The tube is 4:3 landscape. Widescreen = centred 4:3 field on black; side panels stay black (vector games
have nothing to put there unless we add a HUD later).
