// Package riot is the MOS 6532 RIOT (128 bytes RAM, two I/O ports, interval
// timer, PA7 edge interrupt), ported from MAME's mos6530.cpp. Time is the
// owning CPU's cycle count (the RIOT is clocked with the CPU).
package riot

const (
	irqTimer = 0x80
	irqEdge  = 0x40
)

// RIOT is one chip.
type RIOT struct {
	RAM [128]byte

	paOut, paDDR, pbOut, pbDDR byte
	pa7, pa7Dir                byte

	// Inputs: PAIn returns the external level of port A (bits not driven by
	// DDR outputs). PBIn likewise for port B.
	PAIn func() byte
	PBIn func() byte
	// Outputs: called with the full port value whenever it changes.
	PAOut func(v byte)
	PBOut func(v byte)

	ieTimer, irqTimerF bool
	ieEdge, irqEdgeF   bool

	shift    uint
	spinning bool
	target   uint64 // cycle at which the current count expires
	timeout  uint64
}

// New returns a RIOT in its reset state at cycle now.
func New(now uint64) *RIOT {
	r := &RIOT{pa7: 1, shift: 10}
	r.target = now + 256<<10
	return r
}

// Reset is the chip's reset pin.
func (r *RIOT) Reset() {
	r.paOut, r.paDDR, r.pbOut, r.pbDDR = 0, 0, 0, 0
	r.ieTimer, r.ieEdge, r.irqEdgeF = false, false, false
	r.pa7Dir = 0
	r.updatePA()
	r.updatePB()
	r.EdgeDetect()
}

func (r *RIOT) advance(now uint64) {
	for now >= r.target {
		if !r.spinning {
			r.timeout = r.target
			r.irqTimerF = true
		}
		r.spinning = true
		r.target += 256
	}
}

// IRQ is the (active) interrupt output at cycle now.
func (r *RIOT) IRQ(now uint64) bool {
	r.advance(now)
	return (r.ieTimer && r.irqTimerF) || (r.ieEdge && r.irqEdgeF)
}

func (r *RIOT) paValue() byte {
	in := byte(0xff)
	if r.PAIn != nil {
		in = r.PAIn()
	}
	return r.paOut&r.paDDR | in&^r.paDDR
}

func (r *RIOT) updatePA() {
	if r.PAOut != nil {
		r.PAOut((r.paOut & r.paDDR) | (r.paDDR ^ 0xff))
	}
}

func (r *RIOT) updatePB() {
	if r.PBOut != nil {
		r.PBOut((r.pbOut & r.pbDDR) | (r.pbDDR ^ 0xff))
	}
}

// EdgeDetect must be called whenever PA7's input level may have changed.
func (r *RIOT) EdgeDetect() {
	st := (r.paValue() >> 7) & 1
	if r.pa7^st != 0 && r.pa7Dir^st == 0 && !r.irqEdgeF {
		r.irqEdgeF = true
	}
	r.pa7 = st
}

func (r *RIOT) getTimer(now uint64) byte {
	r.advance(now)
	shift := r.shift
	if r.spinning {
		shift = 0
	}
	remain := int64(r.target - now)
	val := byte(remain >> shift)
	if remain&(int64(1)<<shift-1) != 0 {
		return val
	}
	return val - 1
}

func (r *RIOT) timerStart(data byte, now uint64) {
	r.spinning = false
	r.target = now + 1 + uint64(data)<<r.shift
}

// ReadIO reads the I/O block (offset 0..31) at cycle now.
func (r *RIOT) ReadIO(off int, now uint64) byte {
	off &= 0x1f
	if off&4 == 0 {
		switch off & 3 {
		case 0:
			return r.paValue()
		case 1:
			return r.paDDR
		case 2:
			in := byte(0xff)
			if r.PBIn != nil {
				in = r.PBIn()
			}
			return r.pbOut&r.pbDDR | in&^r.pbDDR
		case 3:
			return r.pbDDR
		}
	}
	if off&1 == 0 { // timer read
		v := r.getTimer(now)
		if r.timeout < now { // not cleared when read in the same cycle it fired
			r.irqTimerF = false
			if r.spinning {
				r.timerStart(v, now)
			}
		}
		r.ieTimer = off&8 != 0
		return v
	}
	// interrupt flags
	r.advance(now)
	var v byte
	if r.irqTimerF {
		v |= irqTimer
	}
	if r.irqEdgeF {
		v |= irqEdge
	}
	r.irqEdgeF = false
	return v
}

// WriteIO writes the I/O block at cycle now.
func (r *RIOT) WriteIO(off int, v byte, now uint64) {
	off &= 0x1f
	if off&4 == 0 {
		switch off & 3 {
		case 0:
			r.paOut = v
			r.updatePA()
			r.EdgeDetect()
		case 1:
			r.paDDR = v
			r.updatePA()
			r.EdgeDetect()
		case 2:
			r.pbOut = v
			r.updatePB()
		case 3:
			r.pbDDR = v
			r.updatePB()
		}
		return
	}
	if off&0x10 != 0 { // timer write
		shifts := [4]uint{0, 3, 6, 10}
		r.advance(now)
		r.shift = shifts[off&3]
		r.timerStart(v, now)
		r.irqTimerF = false
		r.ieTimer = off&8 != 0
		return
	}
	// edge detect control
	r.pa7Dir = byte(off & 1)
	r.ieEdge = off&2 != 0
}
