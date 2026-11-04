// Package sound is the Star Wars sound board: a second 6809 with 2K RAM, a
// 6532 RIOT (timer IRQ, latch flags, speech chip handshake), four POKEYs and
// a TMS5220 speech synthesizer, plus the two 8-bit latches to the main
// board. It runs in lockstep with the main CPU (same 1.512 MHz clock) and
// mixes everything to 48 kHz mono.
package sound

import (
	"starwars/internal/m6809"
	"starwars/internal/pokey"
	"starwars/internal/riot"
	"starwars/internal/tms5220"
)

// Clock is the sound CPU (and POKEY) clock.
const Clock = 1512000

// SampleRate is the mixer output rate.
const SampleRate = 48000

// Latches are the two 8-bit latches between the boards.
type Latches struct {
	ToSound, ToMain         byte
	ToSoundFull, ToMainFull bool
}

// Board is the sound board.
type Board struct {
	CPU   *m6809.CPU
	RAM   [0x800]byte
	ROM   *[0x10000]byte
	RIOT  *riot.RIOT
	POKEY [4]*pokey.POKEY
	TMS   *tms5220.TMS5220
	L     *Latches

	// Volumes (1.0 = MAME's routing: POKEYs 0.20 each, speech 0.50).
	SFXGain, SpeechGain float32

	out      []float32 // mixed samples waiting to be played
	pokeySum int64     // sum of POKEY levels over the current output sample
	pokeyN   int64
	frac     int64 // output sample phase, in units of 1/SampleRate CPU cycles
	speech   float32
	speechLP float32
	dcX, dcY float32

	resetPending bool

	// CmdLog, if set, sees each command byte the sound CPU takes (diagnostics).
	CmdLog func(v byte)
}

// New builds the board around the sound ROM image ($4000-$7fff and mirror).
func New(rom *[0x10000]byte, l *Latches) *Board {
	b := &Board{ROM: rom, L: l, SFXGain: 1, SpeechGain: 1}
	b.CPU = &m6809.CPU{Bus: b}
	b.RIOT = riot.New(0)
	for i := range b.POKEY {
		b.POKEY[i] = pokey.New()
	}
	// TMS5220 at 12.096 MHz / 2 / 9 = 672 kHz: one sample per 80 chip clocks
	// = 180 CPU cycles; /READY high for 16 chip clocks (36 CPU cycles) after
	// /WS, 13 us (20 cycles) after /RS.
	b.TMS = tms5220.New(180, 36, 20)
	b.TMS.Out = func(s int16) { b.speech = float32(s) / 32768 }
	b.RIOT.PAIn = func() byte {
		v := byte(0xff)
		if !b.L.ToSoundFull {
			v &^= 0x80
		}
		if !b.L.ToMainFull {
			v &^= 0x40
		}
		if !b.TMS.ReadyQ(b.CPU.Cycles) {
			v &^= 0x04
		}
		return v
	}
	b.RIOT.PAOut = func(v byte) {
		now := b.CPU.Cycles
		b.TMS.WS(v&1 != 0, now)
		b.TMS.RS(v&2 != 0, now)
	}
	b.RIOT.PBIn = func() byte { return b.TMS.Status(b.CPU.Cycles) }
	b.RIOT.PBOut = func(v byte) { b.TMS.Data(v, b.CPU.Cycles) }
	return b
}

// Reset is the sound CPU reset line (main CPU $46e0 pulses it).
func (b *Board) Reset() {
	b.CPU.Reset()
}

// LatchChanged must be called when ToSoundFull changes (PA7 edge interrupt).
func (b *Board) LatchChanged() { b.RIOT.EdgeDetect() }

// Read implements m6809.Bus.
func (b *Board) Read(a uint16) byte {
	switch {
	case a < 0x0800:
		return 0xff
	case a < 0x1000:
		if b.CmdLog != nil && b.L.ToSoundFull {
			b.CmdLog(b.L.ToSound)
		}
		b.L.ToSoundFull = false
		b.RIOT.EdgeDetect()
		return b.L.ToSound
	case a < 0x1080:
		return b.RIOT.RAM[a&0x7f]
	case a < 0x1800:
		if a < 0x10a0 {
			return b.RIOT.ReadIO(int(a&0x1f), b.CPU.Cycles)
		}
		return 0xff
	case a >= 0x2000 && a < 0x2800:
		return b.RAM[a-0x2000]
	case a >= 0x4000 && a < 0x8000, a >= 0xb000:
		return b.ROM[a]
	}
	return 0xff
}

// Write implements m6809.Bus.
func (b *Board) Write(a uint16, v byte) {
	switch {
	case a < 0x0800:
		b.L.ToMain = v
		b.L.ToMainFull = true
	case a >= 0x1000 && a < 0x1080:
		b.RIOT.RAM[a&0x7f] = v
	case a >= 0x1080 && a < 0x10a0:
		b.RIOT.WriteIO(int(a&0x1f), v, b.CPU.Cycles)
	case a >= 0x1800 && a < 0x1840:
		off := int(a & 0x3f)
		chip := (off >> 3) &^ 4
		reg := off&7 | (off&0x20)>>2
		b.POKEY[chip].Write(reg, v)
	case a >= 0x2000 && a < 0x2800:
		b.RAM[a-0x2000] = v
	}
}

// RunTo runs the sound board up to CPU cycle t.
func (b *Board) RunTo(t uint64) {
	c := b.CPU
	for c.Cycles < t {
		c.IRQ = b.RIOT.IRQ(c.Cycles)
		before := c.Cycles
		c.Step()
		n := int(c.Cycles - before)
		if n <= 0 {
			n = 1
			c.Cycles++
		}
		for i := 0; i < n; i++ {
			lv := 0
			for _, p := range b.POKEY {
				p.Step()
				lv += p.Level()
			}
			b.pokeySum += int64(lv)
			b.pokeyN++
			b.frac += SampleRate
			if b.frac >= Clock {
				b.frac -= Clock
				b.emit()
			}
		}
	}
	b.TMS.Run(c.Cycles)
}

func (b *Board) emit() {
	// POKEYs: MAME LEGACY_LINEAR: level * (32767/11/4) / 32768 per chip, routed at 0.20.
	var sfx float32
	if b.pokeyN > 0 {
		sfx = float32(b.pokeySum) / float32(b.pokeyN) * (32767.0 / 11 / 4 / 32768) * 0.20
	}
	b.pokeySum, b.pokeyN = 0, 0
	// Speech: 8.4 kHz zero-order hold smoothed by a one-pole low-pass (~4 kHz).
	b.speechLP += (b.speech - b.speechLP) * 0.42
	v := sfx*b.SFXGain + b.speechLP*0.5*b.SpeechGain
	// DC blocker (POKEY output is unipolar).
	y := v - b.dcX + 0.9995*b.dcY
	b.dcX, b.dcY = v, y
	b.out = append(b.out, y)
}

// TakeSamples returns and clears the mixed output so far.
func (b *Board) TakeSamples(dst []float32) []float32 {
	dst = append(dst, b.out...)
	b.out = b.out[:0]
	return dst
}
