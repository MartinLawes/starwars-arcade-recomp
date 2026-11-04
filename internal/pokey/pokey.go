// Package pokey is the Atari POKEY sound generator (audio part only),
// ported from MAME's pokey.cpp: channel counters with borrow delays, the
// 64 kHz / 15 kHz / CPU-clock bases, 16-bit joined channels, high-pass
// filters, and the 4/5/9/17-bit polynomial counters. Output is MAME's
// LEGACY_LINEAR mix (sum of channel volumes).
package pokey

const (
	notPoly5   = 0x80
	poly4Bit   = 0x40
	pure       = 0x20
	volumeOnly = 0x10

	poly9     = 0x80
	ch1HiClk  = 0x40
	ch3HiClk  = 0x20
	ch12Join  = 0x10
	ch34Join  = 0x08
	ch1Filter = 0x04
	ch2Filter = 0x02
	clk15k    = 0x01

	skReset = 0x03
	div64   = 28
	div15   = 114
)

var poly4, poly5, poly9t, poly17 []uint8

func init() {
	poly4 = polyInit45(4)
	poly5 = polyInit45(5)
	poly9t = polyInit917(9)
	poly17 = polyInit917(17)
}

func polyInit45(size int) []uint8 {
	mask := uint32(1)<<size - 1
	out := make([]uint8, mask)
	var lfsr uint32
	xb := uint(size - 1)
	for i := range out {
		lfsr = lfsr<<1 | (^((lfsr >> 2) ^ (lfsr >> xb)) & 1)
		out[i] = uint8(lfsr & mask & 1)
	}
	return out
}

func polyInit917(size int) []uint8 {
	mask := uint32(1)<<size - 1
	out := make([]uint8, mask)
	lfsr := mask
	for i := range out {
		if size == 17 {
			in8 := (lfsr>>8)&1 ^ (lfsr>>13)&1
			in := lfsr & 1
			lfsr >>= 1
			lfsr = lfsr&0xff7f | in8<<7
			lfsr = in<<16 | lfsr
		} else {
			in := lfsr&1 ^ (lfsr>>5)&1
			lfsr >>= 1
			lfsr = in<<8 | lfsr
		}
		out[i] = uint8(lfsr & 1)
	}
	return out
}

type channel struct {
	audf, audc   uint8
	borrow       int
	counter      int
	output       uint8
	filterSample uint8
}

func (c *channel) reset()  { c.counter = int(c.audf ^ 0xff); c.borrow = 0 }
func (c *channel) sample() { c.filterSample = c.output }
func (c *channel) inc(cycles int) {
	c.counter = (c.counter + 1) & 0xff
	if c.counter == 0 && c.borrow == 0 {
		c.borrow = cycles
	}
}
func (c *channel) checkBorrow() bool {
	if c.borrow > 0 {
		c.borrow--
		return c.borrow == 0
	}
	return false
}

// POKEY is one chip.
type POKEY struct {
	ch              [4]channel
	audctl, skctl   uint8
	p4, p5, p9, p17 int
	clk28, clk114   int
	outRaw          uint32 // 4 x 4-bit channel levels
}

// New returns a chip in its power-on state.
func New() *POKEY {
	p := &POKEY{}
	for i := range p.ch {
		p.ch[i].counter = 0xff
	}
	return p
}

// Write sets register r (0..15).
func (p *POKEY) Write(r int, v byte) {
	switch r {
	case 0, 2, 4, 6:
		p.ch[r/2].audf = v
	case 1, 3, 5, 7:
		p.ch[r/2].audc = v
	case 8:
		p.audctl = v
	case 9: // STIMER
		for i := range p.ch {
			p.ch[i].reset()
			p.ch[i].output = 0
			if i < 2 {
				p.ch[i].filterSample = 1
			} else {
				p.ch[i].filterSample = 0
			}
		}
	case 15: // SKCTL
		p.skctl = v
		if v&skReset == 0 {
			p.p4, p.p5, p.p9, p.p17 = 0, 0, 0, 0
			p.clk28, p.clk114 = 0, 0
		}
	}
	p.mix()
}

func (p *POKEY) process(ch int) {
	c := &p.ch[ch]
	if c.audc&notPoly5 != 0 || poly5[p.p5] != 0 {
		switch {
		case c.audc&pure != 0:
			c.output ^= 1
		case c.audc&poly4Bit != 0:
			c.output = poly4[p.p4]
		case p.audctl&poly9 != 0:
			c.output = poly9t[p.p9]
		default:
			c.output = poly17[p.p17]
		}
	}
}

func (p *POKEY) mix() {
	var sum uint32
	for i := range p.ch {
		c := &p.ch[i]
		if c.output^c.filterSample != 0 || c.audc&volumeOnly != 0 {
			sum |= uint32(c.audc&0x0f) << (4 * i)
		}
	}
	p.outRaw = sum
}

// Level is the current output, 0..60.
func (p *POKEY) Level() int {
	r := p.outRaw
	return int(r&15 + r>>4&15 + r>>8&15 + r>>12&15)
}

// Step advances one chip clock.
func (p *POKEY) Step() {
	changed := false
	if p.skctl&skReset != 0 {
		if p.p4++; p.p4 == 0xf {
			p.p4 = 0
		}
		if p.p5++; p.p5 == 0x1f {
			p.p5 = 0
		}
		if p.p9++; p.p9 == 0x1ff {
			p.p9 = 0
		}
		if p.p17++; p.p17 == 0x1ffff {
			p.p17 = 0
		}
		t28, t114 := false, false
		if p.clk28++; p.clk28 >= div64 {
			p.clk28 = 0
			t28 = true
		}
		if p.clk114++; p.clk114 >= div15 {
			p.clk114 = 0
			t114 = true
		}
		base := t28
		if p.audctl&clk15k != 0 {
			base = t114
		}
		a := p.audctl
		if a&ch1HiClk != 0 {
			if a&ch12Join != 0 {
				p.ch[0].inc(7)
			} else {
				p.ch[0].inc(4)
			}
		} else if base {
			p.ch[0].inc(1)
		}
		if a&ch3HiClk != 0 {
			if a&ch34Join != 0 {
				p.ch[2].inc(7)
			} else {
				p.ch[2].inc(4)
			}
		} else if base {
			p.ch[2].inc(1)
		}
		if base {
			if a&ch12Join == 0 {
				p.ch[1].inc(1)
			}
			if a&ch34Join == 0 {
				p.ch[3].inc(1)
			}
		}
	}
	a := p.audctl
	if p.ch[2].checkBorrow() {
		if a&ch34Join != 0 {
			p.ch[3].inc(1)
		} else {
			p.ch[2].reset()
		}
		p.process(2)
		if a&ch1Filter != 0 {
			p.ch[0].sample()
		} else {
			p.ch[0].filterSample = 1
		}
		changed = true
	}
	if p.ch[3].checkBorrow() {
		if a&ch34Join != 0 {
			p.ch[2].reset()
		}
		p.ch[3].reset()
		p.process(3)
		if a&ch2Filter != 0 {
			p.ch[1].sample()
		} else {
			p.ch[1].filterSample = 1
		}
		changed = true
	}
	if p.ch[0].checkBorrow() {
		if a&ch12Join != 0 {
			p.ch[1].inc(1)
		} else {
			p.ch[0].reset()
		}
		p.process(0)
		changed = true
	}
	if p.ch[1].checkBorrow() {
		if a&ch12Join != 0 {
			p.ch[0].reset()
		}
		p.ch[1].reset()
		p.process(1)
		changed = true
	}
	if changed {
		p.mix()
	}
}
