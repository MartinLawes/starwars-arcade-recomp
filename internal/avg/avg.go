// Package avg is the Atari Analog Vector Generator as wired on Star Wars: a
// 256x4 PROM state machine that walks display lists in main CPU memory and
// moves the beam. It is a port of MAME's avgdvg.cpp (AVG base + Star Wars
// handlers). Output is a list of beam moves in the AVG's own coordinate
// space - never pixels - so the renderer can draw them at any resolution.
package avg

// Beam coordinate space (MAME's Star Wars visible area is 0..250 x 0..280,
// positions are 16.16 fixed point inside it). Y grows downward.
const (
	Width   = 250
	Height  = 280
	centerX = (Width / 2) << 16
	centerY = (Height / 2) << 16
)

// MasterPerCPU is master clocks per main CPU cycle.
const MasterPerCPU = 8

// Point is one beam position. A point with Intensity > 0 means the beam was
// lit travelling from the previous point to this one.
type Point struct {
	X, Y      int32 // 16.16 fixed point, 0..Width / 0..Height
	Color     uint8 // 3-bit RGB: bit 2 = R, bit 1 = G, bit 0 = B
	Intensity uint8 // 0..255 after the Star Wars intensity DAC
}

// Segment is a lit beam stroke. A zero-length segment is a dot.
type Segment struct {
	X0, Y0, X1, Y1 float32 // beam units, 0..Width / 0..Height
	R, G, B        float32 // 0..1 colour after intensity
	Intensity      float32 // 0..1
}

// Mem is where the AVG fetches its display list (main CPU $0000-$3fff).
type Mem func(a uint16) byte

// AVG holds the generator state.
type AVG struct {
	prom [256]byte
	read Mem

	pc       uint16
	sp       uint8
	dvx, dvy uint16
	stack    [4]uint16
	data     uint16
	state    uint8
	scale    uint8
	binScale uint8
	intens   uint8
	color    uint8
	op       uint8
	dvy12    uint8
	intLatch uint8
	timer    uint16
	halt     uint8
	xpos     int32
	ypos     int32

	pts []Point

	running   bool
	haltAt    uint64 // CPU cycle when the halt flag becomes visible
	syncHalt  bool
	clock     uint64 // master clocks consumed by the state machine
	cpuAnchor uint64 // CPU cycle the current run started at

	// Frame receives each finished display list (called from Go or Run).
	Frame func(pts []Point)
	// Lists counts finished display lists.
	Lists int
}

// New builds an AVG from the 136021-109 state PROM.
func New(prom []byte, read Mem) *AVG {
	a := &AVG{read: read, xpos: centerX, ypos: centerY, syncHalt: true}
	copy(a.prom[:], prom)
	return a
}

func (a *AVG) op0() bool { return a.op&1 != 0 }
func (a *AVG) op1() bool { return a.op&2 != 0 }
func (a *AVG) op2() bool { return a.op&4 != 0 }
func b2u(b bool) uint16 {
	if b {
		return 1
	}
	return 0
}

func (a *AVG) stateAddr() uint8 {
	return ((a.state>>4)^1)<<7 | a.op<<4 | a.state&0xf
}

// Halted is IN1 bit 6 at main CPU cycle now.
func (a *AVG) Halted(now uint64) bool {
	if a.running {
		a.Run(now)
	}
	if !a.syncHalt && a.halt != 0 && now >= a.haltAt {
		a.syncHalt = true
	}
	return a.syncHalt
}

// Reset is a write to $4620.
func (a *AVG) Reset() {
	a.state = 0
	a.binScale = 0
	a.scale = 0
	a.color = 0
	a.halt = 1
	a.syncHalt = true
	a.running = false
}

// Go is a write to $4600 at main CPU cycle now.
func (a *AVG) Go(now uint64) {
	if a.running {
		a.Run(now)
	}
	a.pc = 0
	a.sp = 0
	if a.syncHalt && len(a.pts) > 10 {
		a.flush()
	}
	a.halt = 0
	a.syncHalt = false
	a.running = true
	a.clock = 0
	a.cpuAnchor = now
}

func (a *AVG) flush() {
	a.Lists++
	if a.Frame != nil {
		a.Frame(a.pts)
	}
	a.pts = a.pts[:0]
}

func (a *AVG) add(x, y int32, color, intensity uint8) {
	if len(a.pts) < 10000 {
		a.pts = append(a.pts, Point{x, y, color, intensity})
	}
}

// Run advances the state machine up to main CPU cycle now. The AVG reads
// memory as it goes, so the board calls this often (MAME slices it every
// 10000 master clocks).
func (a *AVG) Run(now uint64) {
	if !a.running {
		return
	}
	limit := (now - a.cpuAnchor) * MasterPerCPU
	for a.clock < limit {
		cycles := 0
		a.state = a.state&0x10 | a.prom[a.stateAddr()]&0xf
		if a.state&8 != 0 {
			a.data = uint16(a.read(a.pc))
			switch a.state & 7 {
			case 0:
				a.dvy = a.dvy&0x1f00 | a.data
				a.pc++
			case 1:
				a.dvy12 = uint8(a.data>>4) & 1
				a.op = uint8(a.data >> 5)
				a.intLatch = 0
				a.dvy = uint16(a.dvy12)<<12 | (a.data&0xf)<<8
				a.dvx = 0
				a.pc++
			case 2:
				a.dvx = a.dvx&0x1f00 | a.data
				a.pc++
			case 3:
				a.intLatch = uint8(a.data >> 4)
				a.dvx = uint16(a.intLatch&1)<<12 | (a.data&0xf)<<8 | a.dvx&0xff
				a.pc++
			case 4:
				a.strobe0()
			case 5:
				a.strobe1()
			case 6:
				a.strobe2()
			case 7:
				cycles = a.strobe3()
			}
		}
		if a.halt != 0 && a.state&0x10 == 0 {
			a.haltAt = a.cpuAnchor + (a.clock+uint64(cycles))/MasterPerCPU
		}
		a.state = a.halt<<4 | a.state&0xf
		a.clock += uint64(cycles) + 8
		if a.halt != 0 {
			a.running = false
			return
		}
	}
}

func (a *AVG) strobe0() {
	if a.op0() {
		a.stack[a.sp&3] = a.pc
		return
	}
	for i := 0; ((a.dvy^(a.dvy<<1))&0x1000) == 0 && ((a.dvx^(a.dvx<<1))&0x1000) == 0 && i < 16; i++ {
		a.dvy = a.dvy&0x1000 | (a.dvy<<1)&0x1fff
		a.dvx = a.dvx&0x1000 | (a.dvx<<1)&0x1fff
		a.timer >>= 1
		a.timer |= 0x4000 | b2u(a.op1())<<7
	}
	if a.op1() {
		a.timer &= 0xff
	}
}

func (a *AVG) strobe1() {
	if !a.op2() {
		for i := a.binScale; i > 0; i-- {
			a.timer >>= 1
			a.timer |= 0x4000 | b2u(a.op1())<<7
		}
		if a.op1() {
			a.timer &= 0xff
		}
	}
	if a.op2() {
		if a.op1() {
			a.sp = (a.sp - 1) & 0xf
		} else {
			a.sp = (a.sp + 1) & 0xf
		}
	}
}

func (a *AVG) strobe2() {
	// Star Wars: 8-bit intensity, 4-bit colour field.
	if !a.op2() && a.dvy12 == 0 {
		a.intens = uint8(a.dvy)
		a.color = uint8(a.dvy>>8) & 0xf
	}
	if a.op2() {
		if a.op0() {
			a.pc = a.dvy << 1
		} else {
			a.pc = a.stack[a.sp&3]
		}
	} else if a.dvy12 != 0 {
		a.scale = uint8(a.dvy)
		a.binScale = uint8(a.dvy>>8) & 7
	}
}

// Star Wars intensity DAC: 12k/24k/47k summing network on int_latch bits 3..1.
var dacScale [16]float32

func init() {
	full := float32(1 + 0.5 + 12.0/47.0)
	for l := 0; l < 16; l++ {
		var d float32
		if l&8 != 0 {
			d += 1
		}
		if l&4 != 0 {
			d += 0.5
		}
		if l&2 != 0 {
			d += 12.0 / 47.0
		}
		dacScale[l] = d / full
	}
}

func (a *AVG) strobe3() int {
	cycles := 0
	a.halt = uint8(b2u(a.op0()))
	if !a.op0() && !a.op2() {
		if a.op1() {
			cycles = 0x100 - int(a.timer&0xff)
		} else {
			cycles = 0x8000 - int(a.timer)
		}
		a.timer = 0
		sc := int32(a.scale ^ 0xff)
		a.xpos += ((int32((a.dvx>>3)^0x200) - 0x200) * int32(cycles) * sc) >> 4
		a.ypos -= ((int32((a.dvy>>3)^0x200) - 0x200) * int32(cycles) * sc) >> 4
	}
	if a.op2() {
		cycles = 0x8000 - int(a.timer)
		a.timer = 0
		a.xpos = centerX
		a.ypos = centerY
		a.add(a.xpos, a.ypos, 0, 0)
	}
	if !a.op0() && !a.op2() {
		in := int(dacScale[a.intLatch&0xf]*float32(a.intens) + 0.5)
		a.add(a.xpos, a.ypos, a.color&7, uint8(in))
	}
	return cycles
}

// Segments turns a display list into lit strokes in beam units.
func Segments(pts []Point, out []Segment) []Segment {
	out = out[:0]
	for i := 1; i < len(pts); i++ {
		p := pts[i]
		if p.Intensity == 0 {
			continue
		}
		q := pts[i-1]
		in := float32(p.Intensity) / 255
		var r, g, b float32
		if p.Color&4 != 0 {
			r = in
		}
		if p.Color&2 != 0 {
			g = in
		}
		if p.Color&1 != 0 {
			b = in
		}
		out = append(out, Segment{
			X0: float32(q.X) / 65536, Y0: float32(q.Y) / 65536,
			X1: float32(p.X) / 65536, Y1: float32(p.Y) / 65536,
			R: r, G: g, B: b, Intensity: in,
		})
	}
	return out
}
