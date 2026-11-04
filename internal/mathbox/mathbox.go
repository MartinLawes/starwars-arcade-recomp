// Package mathbox is the Star Wars "matrix processor": a PROM-driven
// microsequencer doing ACC += (A-B)*C over the shared math RAM, plus the
// hardware divider and the free-running PRNG. It is a port of MAME's
// starwars_m.cpp (Steve Baines / Frank Palazzolo), keeping the same integer
// widths and wrap-around so results are bit-identical.
package mathbox

// Microcode strobe bits (PROM bits 15-8).
const (
	sLAC      = 0x01
	sReadACC  = 0x02
	sHalt     = 0x04
	sIncBIC   = 0x08
	sClearACC = 0x10
	sLDC      = 0x20
	sLDB      = 0x40
	sLDA      = 0x80
)

// MasterPerCPU is master clocks per main CPU cycle (12.096 MHz / 1.512 MHz).
const MasterPerCPU = 8

// Mathbox holds the processor, divider and PRNG state.
type Mathbox struct {
	str, mas, am [1024]byte // pre-decoded microcode

	// RAM is the 4K math RAM, main CPU $5000-$5fff.
	RAM [0x1000]byte

	MPA uint16 // PROM address
	BIC uint16 // block index counter, 9 bits
	A   int16
	B   int16
	C   int16
	ACC int32

	runUntil uint64 // main CPU cycle at which MATH_RUN drops

	divisor, dividend, dvdShift, quotient uint16

	lfsr     uint32
	lfsrTime uint64 // CPU cycle the LFSR was last advanced to
	lfsrFrac uint64 // leftover half-clocks

	// Steps counts microinstructions executed (diagnostics).
	Steps uint64
}

// New builds a mathbox from the four PROMs (110,111,112,113 as loaded by
// assets: 4 x 1K, low nibble only).
func New(prom []byte) *Mathbox {
	m := &Mathbox{}
	for i := 0; i < 1024; i++ {
		v := uint16(prom[0xc00+i]&0xf) |
			uint16(prom[0x800+i]&0xf)<<4 |
			uint16(prom[0x400+i]&0xf)<<8 |
			uint16(prom[0x000+i]&0xf)<<12
		m.str[i] = byte(v >> 8)
		m.mas[i] = byte(v & 0x7f)
		m.am[i] = byte(v>>7) & 1
	}
	return m
}

// Reset matches machine reset.
func (m *Mathbox) Reset() {
	m.MPA, m.BIC = 0, 0
	m.runUntil = 0
}

// Running is IN1 bit 7 (MATH_RUN) at main CPU cycle now.
func (m *Mathbox) Running(now uint64) bool { return now < m.runUntil }

func (m *Mathbox) run(now uint64) {
	mptime := uint64(0)
	for stop := 100000; stop > 0; stop-- {
		mptime += 5
		m.Steps++
		st := m.str[m.MPA]
		ip := uint16(m.mas[m.MPA])
		var ma uint16
		if m.am[m.MPA] == 0 {
			ma = ip&3 | (m.BIC&0x1ff)<<2
		} else {
			ma = ip
		}
		mb := int(ma) << 1
		word := int16(uint16(m.RAM[mb])<<8 | uint16(m.RAM[mb+1]))

		if st&sClearACC != 0 {
			m.ACC = 0
		}
		if st&sLAC != 0 {
			m.ACC = int32(word) << 16
		}
		if st&sReadACC != 0 {
			m.RAM[mb+1] = byte(m.ACC >> 16)
			m.RAM[mb] = byte(m.ACC >> 24)
		}
		if st&sHalt != 0 {
			stop = 0
		}
		if st&sIncBIC != 0 {
			m.BIC = (m.BIC + 1) & 0x1ff
		}
		if st&sLDC != 0 {
			m.C = word
			// C: ACC += (((int)(A-B) << 1) * C) << 1, int arithmetic wrapping at 32 bits.
			d := (int64(m.A) - int64(m.B)) << 1
			m.ACC += int32(uint32(int64(uint32(d*int64(m.C))) << 1))
			if m.A < 0 {
				m.A = -1
			} else {
				m.A = 0
			}
			if m.B < 0 {
				m.B = -1
			} else {
				m.B = 0
			}
			mptime += 33
		}
		if st&sLDB != 0 {
			m.B = word
		}
		if st&sLDA != 0 {
			m.A = word
		}
		m.MPA = m.MPA&0x300 | (m.MPA+1)&0xff
		if stop == 0 {
			break
		}
	}
	m.runUntil = now + (mptime+MasterPerCPU-1)/MasterPerCPU
}

// Write handles main CPU writes to $4700-$4707 (off = address & 7).
func (m *Mathbox) Write(off int, v byte, now uint64) {
	switch off {
	case 0:
		m.MPA = uint16(v) << 2
		m.run(now)
	case 1:
		m.BIC = m.BIC&0xff | uint16(v&1)<<8
	case 2:
		m.BIC = m.BIC&0x100 | uint16(v)
	case 4:
		m.divisor = m.divisor&0xff | uint16(v)<<8
		m.dvdShift = m.dividend
		m.quotient = 0
	case 5:
		m.divisor = m.divisor&0xff00 | uint16(v)
		neg := uint32(m.divisor^0xffff) + 1
		for i := 1; i < 16; i++ {
			m.quotient <<= 1
			if (uint32(m.dvdShift)+neg)&0x10000 != 0 {
				m.quotient |= 1
				m.dvdShift = uint16((uint32(m.dvdShift) + neg) << 1)
			} else {
				m.dvdShift <<= 1
			}
		}
	case 6:
		m.dividend = m.dividend&0xff | uint16(v)<<8
	case 7:
		m.dividend = m.dividend&0xff00 | uint16(v)
	}
}

// QuotientHi / QuotientLo are reads of $4700 / $4701.
func (m *Mathbox) QuotientHi() byte { return byte(m.quotient >> 8) }
func (m *Mathbox) QuotientLo() byte { return byte(m.quotient) }

// The PRNG is a 23-bit LFSR, taps at bits 4 and 22, inverted feedback (so
// all-zero is a valid start), clocked at ~3 MHz (2 per main CPU cycle).
// The CPU reads bits 15-8.
func (m *Mathbox) advance(now uint64) {
	if now <= m.lfsrTime {
		return
	}
	n := (now - m.lfsrTime) * 2
	m.lfsrTime = now
	// The sequence repeats every 8388607 clocks; skip whole loops.
	n %= 8388607
	for ; n > 0; n-- {
		fb := ^((m.lfsr >> 22) ^ (m.lfsr >> 4)) & 1
		m.lfsr = (m.lfsr<<1 | fb) & 0x7fffff
	}
}

// PRNG is a read of $4703 at main CPU cycle now.
func (m *Mathbox) PRNG(now uint64) byte {
	m.advance(now)
	return byte(m.lfsr >> 8)
}

// PRNGReset is LS259 bit 5: while asserted the shift register is held at zero.
func (m *Mathbox) PRNGReset(on bool, now uint64) {
	m.advance(now)
	if on {
		m.lfsr = 0
	}
}
