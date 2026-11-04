// Package board ties the Star Wars main board together: main 6809, mathbox,
// AVG, NVRAM, inputs, LS259 latch and the main/sound latches.
package board

import (
	"starwars/internal/assets"
	"starwars/internal/avg"
	"starwars/internal/m6809"
	"starwars/internal/mathbox"
	"starwars/internal/sound"
)

// Timing, in main CPU cycles (1.512 MHz).
const (
	CPUHz     = 1512000
	IRQCycles = 6144 // 12.096 MHz / 4096 / 12
	avgSlice  = 1250 // 10000 master clocks, as MAME slices the AVG
)

// Inputs are the player controls, sampled by the board when read.
type Inputs struct {
	Coin1, Coin2, Service, Tilt, Test bool
	Fire1, Fire2, Fire3, Fire4        bool // trigger / thumb buttons
	DiagStep                          bool
	YokeX, YokeY                      uint8 // 0..255, 128 = centre (X = yaw, Y = pitch)
}

// Board is the main board.
type Board struct {
	ROM     *assets.ROMs
	CPU     *m6809.CPU
	Math    *mathbox.Mathbox
	AVG     *avg.AVG
	RAM     [0x3000]byte // $0000-$2fff
	RAM2    [0x0800]byte // $4800-$4fff
	NVRAM   [0x100]byte  // X2212, 4 bits per cell
	NVDirty bool

	In         Inputs
	DSW0, DSW1 byte

	bank     int
	latch259 byte
	adcChan  int
	adcValue byte

	// Sound board and the two latches between the boards.
	L   sound.Latches
	Snd *sound.Board

	nextIRQ uint64
	nextAVG uint64
}

// New builds a board around a loaded ROM set.
func New(r *assets.ROMs) *Board {
	b := &Board{ROM: r, DSW0: 0x88, DSW1: 0x02}
	b.Math = mathbox.New(r.Math[:])
	b.AVG = avg.New(r.AVGProm[:], b.avgRead)
	b.CPU = &m6809.CPU{Bus: b}
	b.Snd = sound.New(&r.Sound, &b.L)
	b.In.YokeX, b.In.YokeY = 128, 128
	return b
}

// Reset is a power-on reset.
func (b *Board) Reset() {
	b.Math.Reset()
	b.AVG.Reset()
	b.CPU.Reset()
	b.Snd.CPU.Cycles = b.CPU.Cycles
	b.Snd.Reset()
	b.nextIRQ = b.CPU.Cycles + IRQCycles
	b.nextAVG = b.CPU.Cycles + avgSlice
}

// The AVG sees main CPU $0000-$3fff: vector RAM and vector ROM.
func (b *Board) avgRead(a uint16) byte {
	a &= 0x3fff
	if a < 0x3000 {
		return b.RAM[a]
	}
	return b.ROM.Vector[a-0x3000]
}

func active(low bool, bit byte) byte {
	if low {
		return 0
	}
	return bit
}

// Read implements m6809.Bus.
func (b *Board) Read(a uint16) byte {
	now := b.CPU.Cycles
	switch {
	case a < 0x3000:
		return b.RAM[a]
	case a < 0x4000:
		return b.ROM.Vector[a-0x3000]
	case a < 0x4300:
		return 0xff
	case a < 0x4320: // IN0, active low
		in := &b.In
		return active(in.Coin2, 0x01) | active(in.Coin1, 0x02) | active(in.Service, 0x04) |
			active(in.Tilt, 0x08) | active(in.Test, 0x10) | 0x20 | active(in.Fire4, 0x40) | active(in.Fire1, 0x80)
	case a < 0x4340: // IN1
		v := active(b.In.DiagStep, 0x04) | active(b.In.Fire3, 0x10) | active(b.In.Fire2, 0x20)
		if b.AVG.Halted(now) {
			v |= 0x40
		}
		if b.Math.Running(now) {
			v |= 0x80
		}
		return v
	case a < 0x4360:
		return b.DSW0
	case a < 0x4380:
		return b.DSW1
	case a < 0x43a0:
		return b.adcValue
	case a == 0x4400:
		b.Snd.RunTo(now)
		b.L.ToMainFull = false
		return b.L.ToMain
	case a == 0x4401:
		b.Snd.RunTo(now)
		var v byte
		if b.L.ToSoundFull {
			v |= 0x80
		}
		if b.L.ToMainFull {
			v |= 0x40
		}
		return v
	case a >= 0x4500 && a < 0x4600:
		return b.NVRAM[a&0xff] | 0xf0
	case a == 0x4700:
		return b.Math.QuotientHi()
	case a == 0x4701:
		return b.Math.QuotientLo()
	case a == 0x4703:
		return b.Math.PRNG(now)
	case a >= 0x4800 && a < 0x5000:
		return b.RAM2[a-0x4800]
	case a >= 0x5000 && a < 0x6000:
		return b.Math.RAM[a-0x5000]
	case a >= 0x6000 && a < 0x8000:
		if b.bank == 1 {
			return b.ROM.Main[0x10000+int(a-0x6000)]
		}
		return b.ROM.Main[a]
	case a >= 0x8000:
		return b.ROM.Main[a]
	}
	return 0xff
}

// Write implements m6809.Bus.
func (b *Board) Write(a uint16, v byte) {
	now := b.CPU.Cycles
	switch {
	case a < 0x3000:
		if b.AVG != nil {
			b.AVG.Run(now) // AVG must see RAM as it was before this write
		}
		b.RAM[a] = v
	case a == 0x4400:
		b.Snd.RunTo(now)
		b.L.ToSound = v
		b.L.ToSoundFull = true
		b.Snd.LatchChanged()
	case a >= 0x4500 && a < 0x4600:
		if b.NVRAM[a&0xff] != v&0x0f {
			b.NVDirty = true
		}
		b.NVRAM[a&0xff] = v & 0x0f
	case a >= 0x4600 && a < 0x4620:
		b.AVG.Go(now)
	case a >= 0x4620 && a < 0x4640:
		b.AVG.Reset()
	case a >= 0x4640 && a < 0x4660: // watchdog
	case a >= 0x4660 && a < 0x4680:
		b.CPU.IRQ = false
	case a >= 0x4680 && a < 0x46a0:
		bit := a & 7
		on := v&0x80 != 0
		if on {
			b.latch259 |= 1 << bit
		} else {
			b.latch259 &^= 1 << bit
		}
		switch bit {
		case 4:
			b.bank = int(v >> 7)
		case 5:
			b.Math.PRNGReset(on, now)
		}
	case a >= 0x46a0 && a < 0x46c0: // NVRAM store (we persist continuously)
	case a >= 0x46c0 && a < 0x46c4:
		b.adcChan = int(a & 3)
		switch b.adcChan {
		case 0:
			b.adcValue = b.In.YokeY
		case 1:
			b.adcValue = b.In.YokeX
		default:
			b.adcValue = 0
		}
	case a == 0x46e0:
		b.Snd.RunTo(now)
		b.L.ToSoundFull = false
		b.L.ToMainFull = false
		b.Snd.Reset()
		b.Snd.LatchChanged()
	case a >= 0x4700 && a < 0x4708:
		b.Math.Write(int(a&7), v, now)
	case a >= 0x4800 && a < 0x5000:
		b.RAM2[a-0x4800] = v
	case a >= 0x5000 && a < 0x6000:
		b.Math.RAM[a-0x5000] = v
	}
}

// RunCycles runs the main CPU for n cycles, raising the periodic IRQ and
// keeping the AVG in step.
func (b *Board) RunCycles(n uint64) {
	end := b.CPU.Cycles + n
	for b.CPU.Cycles < end {
		b.CPU.Step()
		now := b.CPU.Cycles
		b.Snd.RunTo(now)
		if now >= b.nextIRQ {
			b.CPU.IRQ = true
			b.nextIRQ += IRQCycles
		}
		if now >= b.nextAVG {
			b.AVG.Run(now)
			b.nextAVG = now + avgSlice
		}
	}
}
