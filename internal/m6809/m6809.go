// Package m6809 is a Motorola 6809 interpreter. It is the reference ("oracle")
// that the recompiled Star Wars code is verified against.
package m6809

// Bus is the CPU's view of memory.
type Bus interface {
	Read(a uint16) byte
	Write(a uint16, v byte)
}

// Condition code bits.
const (
	CC_C = 0x01
	CC_V = 0x02
	CC_Z = 0x04
	CC_N = 0x08
	CC_I = 0x10
	CC_H = 0x20
	CC_F = 0x40
	CC_E = 0x80
)

// CPU is the register file and interrupt state.
type CPU struct {
	A, B, DP, CC byte
	X, Y, U, S   uint16
	PC           uint16
	Bus          Bus
	Cycles       uint64

	IRQ, FIRQ, NMI bool // input lines (level; NMI is edge-latched by SetNMI)
	nmiPending     bool
	nmiArmed       bool

	Waiting bool // inside SYNC or CWAI
	cwai    bool
	Halted  bool
	Illegal func(c *CPU, pc uint16, op byte)
}

func (c *CPU) D() uint16 { return uint16(c.A)<<8 | uint16(c.B) }
func (c *CPU) SetD(v uint16) {
	c.A = byte(v >> 8)
	c.B = byte(v)
}

func (c *CPU) rd(a uint16) byte     { return c.Bus.Read(a) }
func (c *CPU) wr(a uint16, v byte)  { c.Bus.Write(a, v) }
func (c *CPU) rd16(a uint16) uint16 { return uint16(c.rd(a))<<8 | uint16(c.rd(a+1)) }
func (c *CPU) wr16(a uint16, v uint16) {
	c.wr(a, byte(v>>8))
	c.wr(a+1, byte(v))
}
func (c *CPU) fetch() byte {
	v := c.rd(c.PC)
	c.PC++
	return v
}
func (c *CPU) fetch16() uint16 {
	h := c.fetch()
	l := c.fetch()
	return uint16(h)<<8 | uint16(l)
}

// Reset loads PC from the reset vector.
func (c *CPU) Reset() {
	c.CC = CC_I | CC_F
	c.DP = 0
	c.PC = c.rd16(0xfffe)
	c.Waiting, c.cwai = false, false
}

// SetNMI drives the NMI line; a falling-to-rising transition latches an NMI.
func (c *CPU) SetNMI(v bool) {
	if v && !c.NMI && c.nmiArmed {
		c.nmiPending = true
	}
	c.NMI = v
}

func (c *CPU) flag(m byte, on bool) {
	if on {
		c.CC |= m
	} else {
		c.CC &^= m
	}
}
func (c *CPU) nz8(v byte) {
	c.flag(CC_N, v&0x80 != 0)
	c.flag(CC_Z, v == 0)
}
func (c *CPU) nz16(v uint16) {
	c.flag(CC_N, v&0x8000 != 0)
	c.flag(CC_Z, v == 0)
}

func (c *CPU) pushS8(v byte) { c.S--; c.wr(c.S, v) }
func (c *CPU) pushS16(v uint16) {
	c.pushS8(byte(v))
	c.pushS8(byte(v >> 8))
}
func (c *CPU) pullS8() byte {
	v := c.rd(c.S)
	c.S++
	return v
}
func (c *CPU) pullS16() uint16 {
	h := c.pullS8()
	l := c.pullS8()
	return uint16(h)<<8 | uint16(l)
}
func (c *CPU) pushU8(v byte) { c.U--; c.wr(c.U, v) }
func (c *CPU) pushU16(v uint16) {
	c.pushU8(byte(v))
	c.pushU8(byte(v >> 8))
}
func (c *CPU) pullU8() byte {
	v := c.rd(c.U)
	c.U++
	return v
}
func (c *CPU) pullU16() uint16 {
	h := c.pullU8()
	l := c.pullU8()
	return uint16(h)<<8 | uint16(l)
}

func (c *CPU) pushAll() {
	c.pushS16(c.PC)
	c.pushS16(c.U)
	c.pushS16(c.Y)
	c.pushS16(c.X)
	c.pushS8(c.DP)
	c.pushS8(c.B)
	c.pushS8(c.A)
	c.pushS8(c.CC)
}

// interrupts; returns cycles consumed (0 if none taken).
func (c *CPU) serviceInterrupts() int {
	if c.nmiPending {
		c.nmiPending = false
		if !c.cwai {
			c.CC |= CC_E
			c.pushAll()
		}
		c.CC |= CC_I | CC_F
		c.PC = c.rd16(0xfffc)
		c.Waiting, c.cwai = false, false
		return 19
	}
	if c.FIRQ && c.CC&CC_F == 0 {
		if !c.cwai {
			c.CC &^= CC_E
			c.pushS16(c.PC)
			c.pushS8(c.CC)
		}
		c.CC |= CC_I | CC_F
		c.PC = c.rd16(0xfff6)
		c.Waiting, c.cwai = false, false
		return 10
	}
	if c.IRQ && c.CC&CC_I == 0 {
		if !c.cwai {
			c.CC |= CC_E
			c.pushAll()
		}
		c.CC |= CC_I
		c.PC = c.rd16(0xfff8)
		c.Waiting, c.cwai = false, false
		return 19
	}
	return 0
}

func (c *CPU) reg16(i byte) *uint16 {
	switch i & 3 {
	case 0:
		return &c.X
	case 1:
		return &c.Y
	case 2:
		return &c.U
	}
	return &c.S
}

// indexed decodes an indexed postbyte and returns the effective address and
// the extra cycles it costs.
func (c *CPU) indexed() (uint16, int) {
	pb := c.fetch()
	r := c.reg16(pb >> 5)
	if pb&0x80 == 0 {
		off := int16(int8(pb<<3)) >> 3
		return *r + uint16(off), 1
	}
	var ea uint16
	cy := 0
	switch pb & 0x0f {
	case 0x0:
		ea = *r
		*r++
		cy = 2
	case 0x1:
		ea = *r
		*r += 2
		cy = 3
	case 0x2:
		*r--
		ea = *r
		cy = 2
	case 0x3:
		*r -= 2
		ea = *r
		cy = 3
	case 0x4:
		ea = *r
	case 0x5:
		ea = *r + uint16(int8(c.B))
		cy = 1
	case 0x6:
		ea = *r + uint16(int8(c.A))
		cy = 1
	case 0x8:
		ea = *r + uint16(int8(c.fetch()))
		cy = 1
	case 0x9:
		ea = *r + c.fetch16()
		cy = 4
	case 0xb:
		ea = *r + c.D()
		cy = 4
	case 0xc:
		o := int8(c.fetch())
		ea = c.PC + uint16(o)
		cy = 1
	case 0xd:
		o := c.fetch16()
		ea = c.PC + o
		cy = 5
	case 0xf:
		ea = c.fetch16()
		cy = 5
	default:
		ea = 0
	}
	if pb&0x10 != 0 {
		ea = c.rd16(ea)
		cy += 3
	}
	return ea, cy
}

// ---- ALU helpers --------------------------------------------------------

func (c *CPU) add8(a, b byte, carry byte) byte {
	r := uint16(a) + uint16(b) + uint16(carry)
	res := byte(r)
	c.flag(CC_H, (a^b^res)&0x10 != 0)
	c.flag(CC_C, r > 0xff)
	c.flag(CC_V, (a^res)&(b^res)&0x80 != 0)
	c.nz8(res)
	return res
}
func (c *CPU) sub8(a, b byte, carry byte) byte {
	r := int(a) - int(b) - int(carry)
	res := byte(r)
	c.flag(CC_C, r < 0)
	c.flag(CC_V, (a^b)&(a^res)&0x80 != 0)
	c.nz8(res)
	return res
}
func (c *CPU) add16(a, b uint16) uint16 {
	r := uint32(a) + uint32(b)
	res := uint16(r)
	c.flag(CC_C, r > 0xffff)
	c.flag(CC_V, (a^res)&(b^res)&0x8000 != 0)
	c.nz16(res)
	return res
}
func (c *CPU) sub16(a, b uint16) uint16 {
	r := int(a) - int(b)
	res := uint16(r)
	c.flag(CC_C, r < 0)
	c.flag(CC_V, (a^b)&(a^res)&0x8000 != 0)
	c.nz16(res)
	return res
}
func (c *CPU) logic(v byte) {
	c.nz8(v)
	c.CC &^= CC_V
}

// rmw applies a read-modify-write operation to a byte.
func (c *CPU) rmw(op byte, v byte) byte {
	var r byte
	switch op {
	case 0x0: // NEG
		r = byte(0 - int(v))
		c.flag(CC_C, v != 0)
		c.flag(CC_V, v == 0x80)
		c.nz8(r)
	case 0x3: // COM
		r = ^v
		c.nz8(r)
		c.CC &^= CC_V
		c.CC |= CC_C
	case 0x4: // LSR
		c.flag(CC_C, v&1 != 0)
		r = v >> 1
		c.CC &^= CC_N
		c.flag(CC_Z, r == 0)
	case 0x6: // ROR
		cin := c.CC & CC_C
		c.flag(CC_C, v&1 != 0)
		r = v>>1 | cin<<7
		c.nz8(r)
	case 0x7: // ASR
		c.flag(CC_C, v&1 != 0)
		r = v>>1 | v&0x80
		c.nz8(r)
	case 0x8: // ASL
		c.flag(CC_C, v&0x80 != 0)
		r = v << 1
		c.flag(CC_V, (v^r)&0x80 != 0)
		c.nz8(r)
	case 0x9: // ROL
		cin := c.CC & CC_C
		c.flag(CC_C, v&0x80 != 0)
		r = v<<1 | cin
		c.flag(CC_V, (v^r)&0x80 != 0)
		c.nz8(r)
	case 0xa: // DEC
		r = v - 1
		c.flag(CC_V, v == 0x80)
		c.nz8(r)
	case 0xc: // INC
		r = v + 1
		c.flag(CC_V, v == 0x7f)
		c.nz8(r)
	case 0xd: // TST
		r = v
		c.nz8(r)
		c.CC &^= CC_V
	case 0xf: // CLR
		r = 0
		c.CC &^= CC_N | CC_V | CC_C
		c.CC |= CC_Z
	}
	return r
}

func (c *CPU) daa() {
	cf := byte(0)
	lo := c.A & 0x0f
	hi := c.A & 0xf0
	if lo > 9 || c.CC&CC_H != 0 {
		cf |= 0x06
	}
	if hi > 0x80 && lo > 9 || hi > 0x90 || c.CC&CC_C != 0 {
		cf |= 0x60
	}
	r := uint16(c.A) + uint16(cf)
	c.A = byte(r)
	c.nz8(c.A)
	if r&0x100 != 0 {
		c.CC |= CC_C
	}
}

func (c *CPU) branchCond(op byte) bool {
	n := c.CC&CC_N != 0
	z := c.CC&CC_Z != 0
	v := c.CC&CC_V != 0
	cy := c.CC&CC_C != 0
	switch op & 0x0f {
	case 0x0:
		return true
	case 0x1:
		return false
	case 0x2:
		return !cy && !z
	case 0x3:
		return cy || z
	case 0x4:
		return !cy
	case 0x5:
		return cy
	case 0x6:
		return !z
	case 0x7:
		return z
	case 0x8:
		return !v
	case 0x9:
		return v
	case 0xa:
		return !n
	case 0xb:
		return n
	case 0xc:
		return n == v
	case 0xd:
		return n != v
	case 0xe:
		return !z && n == v
	}
	return z || n != v
}

func (c *CPU) getReg(i byte) uint16 {
	switch i {
	case 0:
		return c.D()
	case 1:
		return c.X
	case 2:
		return c.Y
	case 3:
		return c.U
	case 4:
		return c.S
	case 5:
		return c.PC
	case 8:
		return 0xff00 | uint16(c.A)
	case 9:
		return 0xff00 | uint16(c.B)
	case 10:
		return 0xff00 | uint16(c.CC)
	case 11:
		return 0xff00 | uint16(c.DP)
	}
	return 0xffff
}
func (c *CPU) setReg(i byte, v uint16) {
	switch i {
	case 0:
		c.SetD(v)
	case 1:
		c.X = v
	case 2:
		c.Y = v
	case 3:
		c.U = v
	case 4:
		c.S = v
	case 5:
		c.PC = v
	case 8:
		c.A = byte(v)
	case 9:
		c.B = byte(v)
	case 10:
		c.CC = byte(v)
	case 11:
		c.DP = byte(v)
	}
}

// Step executes one instruction (or services one interrupt) and returns the
// cycles it took.
func (c *CPU) Step() int {
	c.nmiArmed = true
	if c.Waiting {
		if n := c.serviceInterrupts(); n > 0 {
			c.Cycles += uint64(n)
			return n
		}
		// SYNC resumes on any interrupt line even when masked
		if !c.cwai && (c.IRQ || c.FIRQ || c.nmiPending) {
			c.Waiting = false
		} else {
			c.Cycles += 1
			return 1
		}
	} else if n := c.serviceInterrupts(); n > 0 {
		c.Cycles += uint64(n)
		return n
	}
	pc0 := c.PC
	op := c.fetch()
	cy := c.exec(op, pc0)
	c.Cycles += uint64(cy)
	return cy
}

func (c *CPU) exec(op byte, pc0 uint16) int {
	page := 0
	if op == 0x10 || op == 0x11 {
		page = int(op&1) + 1
		op = c.fetch()
	}
	switch {
	case op < 0x10:
		// direct RMW
		if op == 0x0e { // JMP
			c.PC = uint16(c.DP)<<8 | uint16(c.fetch())
			return 3
		}
		if op == 0x01 || op == 0x02 || op == 0x05 || op == 0x0b {
			return c.illegal(pc0, op)
		}
		ea := uint16(c.DP)<<8 | uint16(c.fetch())
		c.rmwMem(ea, op)
		return 6
	case op < 0x20:
		return c.misc1x(op, page, pc0)
	case op < 0x30:
		return c.branch(op, page)
	case op < 0x40:
		return c.misc3x(op, page, pc0)
	case op < 0x60: // inherent A/B
		return c.inherentAB(op, pc0)
	case op < 0x70: // indexed RMW
		ea, x := c.indexed()
		if op&0x0f == 0x0e {
			c.PC = ea
			return 3 + x
		}
		if illegalRMW(op) {
			return c.illegal(pc0, op)
		}
		c.rmwMem(ea, op&0x0f)
		return 6 + x
	case op < 0x80: // extended RMW
		ea := c.fetch16()
		if op&0x0f == 0x0e {
			c.PC = ea
			return 4
		}
		if illegalRMW(op) {
			return c.illegal(pc0, op)
		}
		c.rmwMem(ea, op&0x0f)
		return 7
	}
	return c.alu(op, page, pc0)
}

func (c *CPU) rmwMem(ea uint16, n byte) {
	v := c.rmw(n, c.rd(ea))
	if n != 0x0d { // TST only reads
		c.wr(ea, v)
	}
}

func illegalRMW(op byte) bool {
	switch op & 0x0f {
	case 1, 2, 5, 0xb:
		return true
	}
	return false
}

func (c *CPU) illegal(pc0 uint16, op byte) int {
	if c.Illegal != nil {
		c.Illegal(c, pc0, op)
	}
	return 2
}

func (c *CPU) inherentAB(op byte, pc0 uint16) int {
	reg := &c.A
	if op >= 0x50 {
		reg = &c.B
	}
	n := op & 0x0f
	if illegalRMW(n) {
		return c.illegal(pc0, op)
	}
	if n == 0x0e {
		return c.illegal(pc0, op)
	}
	*reg = c.rmw(n, *reg)
	return 2
}

func (c *CPU) branch(op byte, page int) int {
	if page == 1 { // long branch
		o := c.fetch16()
		if c.branchCond(op) {
			c.PC += o
			return 6
		}
		return 5
	}
	o := int8(c.fetch())
	if c.branchCond(op) {
		c.PC += uint16(int16(o))
	}
	return 3
}

func (c *CPU) misc1x(op byte, page int, pc0 uint16) int {
	switch op {
	case 0x12:
		return 2
	case 0x13: // SYNC
		c.Waiting = true
		return 4
	case 0x16: // LBRA
		o := c.fetch16()
		c.PC += o
		return 5
	case 0x17: // LBSR
		o := c.fetch16()
		c.pushS16(c.PC)
		c.PC += o
		return 9
	case 0x19:
		c.daa()
		return 2
	case 0x1a:
		c.CC |= c.fetch()
		return 3
	case 0x1c:
		c.CC &= c.fetch()
		return 3
	case 0x1d: // SEX
		if c.B&0x80 != 0 {
			c.A = 0xff
		} else {
			c.A = 0
		}
		c.nz16(c.D())
		c.CC &^= CC_V
		return 2
	case 0x1e: // EXG
		p := c.fetch()
		a, b := c.getReg(p>>4), c.getReg(p&15)
		c.setReg(p>>4, b)
		c.setReg(p&15, a)
		return 8
	case 0x1f: // TFR
		p := c.fetch()
		c.setReg(p&15, c.getReg(p>>4))
		return 6
	}
	return c.illegal(pc0, op)
}

func (c *CPU) misc3x(op byte, page int, pc0 uint16) int {
	switch op {
	case 0x30, 0x31, 0x32, 0x33:
		ea, x := c.indexed()
		switch op {
		case 0x30:
			c.X = ea
			c.flag(CC_Z, ea == 0)
		case 0x31:
			c.Y = ea
			c.flag(CC_Z, ea == 0)
		case 0x32:
			c.S = ea
		case 0x33:
			c.U = ea
		}
		return 4 + x
	case 0x34, 0x36: // PSHS / PSHU
		m := c.fetch()
		cy := 5
		push8, push16 := c.pushS8, c.pushS16
		other := c.U
		if op == 0x36 {
			push8, push16 = c.pushU8, c.pushU16
			other = c.S
		}
		if m&0x80 != 0 {
			push16(c.PC)
			cy += 2
		}
		if m&0x40 != 0 {
			push16(other)
			cy += 2
		}
		if m&0x20 != 0 {
			push16(c.Y)
			cy += 2
		}
		if m&0x10 != 0 {
			push16(c.X)
			cy += 2
		}
		if m&0x08 != 0 {
			push8(c.DP)
			cy++
		}
		if m&0x04 != 0 {
			push8(c.B)
			cy++
		}
		if m&0x02 != 0 {
			push8(c.A)
			cy++
		}
		if m&0x01 != 0 {
			push8(c.CC)
			cy++
		}
		return cy
	case 0x35, 0x37: // PULS / PULU
		m := c.fetch()
		cy := 5
		pull8, pull16 := c.pullS8, c.pullS16
		if op == 0x37 {
			pull8, pull16 = c.pullU8, c.pullU16
		}
		if m&0x01 != 0 {
			c.CC = pull8()
			cy++
		}
		if m&0x02 != 0 {
			c.A = pull8()
			cy++
		}
		if m&0x04 != 0 {
			c.B = pull8()
			cy++
		}
		if m&0x08 != 0 {
			c.DP = pull8()
			cy++
		}
		if m&0x10 != 0 {
			c.X = pull16()
			cy += 2
		}
		if m&0x20 != 0 {
			c.Y = pull16()
			cy += 2
		}
		if m&0x40 != 0 {
			if op == 0x35 {
				c.U = pull16()
			} else {
				c.S = pull16()
			}
			cy += 2
		}
		if m&0x80 != 0 {
			c.PC = pull16()
			cy += 2
		}
		return cy
	case 0x39:
		c.PC = c.pullS16()
		return 5
	case 0x3a:
		c.X += uint16(c.B)
		return 3
	case 0x3b: // RTI
		c.CC = c.pullS8()
		cy := 6
		if c.CC&CC_E != 0 {
			c.A = c.pullS8()
			c.B = c.pullS8()
			c.DP = c.pullS8()
			c.X = c.pullS16()
			c.Y = c.pullS16()
			c.U = c.pullS16()
			cy = 15
		}
		c.PC = c.pullS16()
		return cy
	case 0x3c: // CWAI
		m := c.fetch()
		c.CC &= m
		c.CC |= CC_E
		c.pushAll()
		c.Waiting, c.cwai = true, true
		return 20
	case 0x3d: // MUL
		r := uint16(c.A) * uint16(c.B)
		c.SetD(r)
		c.flag(CC_Z, r == 0)
		c.flag(CC_C, r&0x80 != 0)
		return 11
	case 0x3f: // SWI / SWI2 / SWI3
		old := c.CC & (CC_I | CC_F)
		c.CC |= CC_E
		c.pushAll()
		vec := uint16(0xfffa)
		switch page {
		case 0:
			c.CC |= CC_I | CC_F
		case 1:
			vec = 0xfff4
			c.CC = c.CC&^(CC_I|CC_F) | old
		case 2:
			vec = 0xfff2
			c.CC = c.CC&^(CC_I|CC_F) | old
		}
		c.PC = c.rd16(vec)
		return 19
	}
	return c.illegal(pc0, op)
}

// alu handles opcodes 0x80-0xff.
func (c *CPU) alu(op byte, page int, pc0 uint16) int {
	mode := (op >> 4) & 3 // 0 imm, 1 direct, 2 indexed, 3 extended
	bside := op >= 0xc0
	n := op & 0x0f

	// operand address (or immediate flag)
	var ea uint16
	xcy := 0
	wide := false
	switch n {
	case 0x3, 0xc, 0xe, 0xf, 0xd:
		wide = true
	}
	if !bside {
		switch n {
		case 0x3, 0xc, 0xe, 0xf:
			wide = true
		default:
			wide = false
		}
	} else {
		switch n {
		case 0x3, 0xc, 0xd, 0xe, 0xf:
			wide = true
		default:
			wide = false
		}
	}
	// BSR/JSR in A column (0x8d, 0x9d, 0xad, 0xbd)
	if !bside && n == 0xd {
		switch mode {
		case 0:
			o := int8(c.fetch())
			c.pushS16(c.PC)
			c.PC += uint16(int16(o))
			return 7
		case 1:
			ea = uint16(c.DP)<<8 | uint16(c.fetch())
			c.pushS16(c.PC)
			c.PC = ea
			return 7
		case 2:
			var x int
			ea, x = c.indexed()
			c.pushS16(c.PC)
			c.PC = ea
			return 7 + x
		}
		ea = c.fetch16()
		c.pushS16(c.PC)
		c.PC = ea
		return 8
	}
	imm := mode == 0
	base := 0
	if !imm {
		switch mode {
		case 1:
			ea = uint16(c.DP)<<8 | uint16(c.fetch())
			base = 4
		case 2:
			ea, xcy = c.indexed()
			base = 4
		case 3:
			ea = c.fetch16()
			base = 5
		}
	}
	// 8-bit read
	get8 := func() byte {
		if imm {
			return c.fetch()
		}
		return c.rd(ea)
	}
	get16 := func() uint16 {
		if imm {
			return c.fetch16()
		}
		return c.rd16(ea)
	}
	cyc := func(imm8, mem8, imm16, mem16 int) int {
		if imm {
			if wide {
				return imm16
			}
			return imm8
		}
		if wide {
			return mem16 + xcy + (base - 4)
		}
		return mem8 + xcy + (base - 4)
	}
	_ = cyc

	if !bside {
		switch n {
		case 0x0:
			c.A = c.sub8(c.A, get8(), 0)
		case 0x1:
			c.sub8(c.A, get8(), 0)
		case 0x2:
			c.A = c.sub8(c.A, get8(), c.CC&CC_C)
		case 0x3: // SUBD / CMPD(page 2) / CMPU (page 3)
			v := get16()
			switch page {
			case 0:
				c.SetD(c.sub16(c.D(), v))
			case 1:
				c.sub16(c.D(), v)
			default:
				c.sub16(c.U, v)
			}
		case 0x4:
			c.A &= get8()
			c.logic(c.A)
		case 0x5:
			c.logic(c.A & get8())
		case 0x6:
			c.A = get8()
			c.logic(c.A)
		case 0x7:
			if imm {
				return c.illegal(pc0, op)
			}
			c.logic(c.A)
			c.wr(ea, c.A)
		case 0x8:
			c.A ^= get8()
			c.logic(c.A)
		case 0x9:
			c.A = c.add8(c.A, get8(), c.CC&CC_C)
		case 0xa:
			c.A |= get8()
			c.logic(c.A)
		case 0xb:
			c.A = c.add8(c.A, get8(), 0)
		case 0xc: // CMPX / CMPY / CMPS
			v := get16()
			switch page {
			case 0:
				c.sub16(c.X, v)
			case 1:
				c.sub16(c.Y, v)
			default:
				c.sub16(c.S, v)
			}
		case 0xe: // LDX / LDY
			v := get16()
			if page == 0 {
				c.X = v
			} else {
				c.Y = v
			}
			c.nz16(v)
			c.CC &^= CC_V
		case 0xf: // STX / STY
			if imm {
				return c.illegal(pc0, op)
			}
			v := c.X
			if page == 1 {
				v = c.Y
			}
			c.nz16(v)
			c.CC &^= CC_V
			c.wr16(ea, v)
		}
		return aluCycles(false, n, mode, page, xcy)
	}
	switch n {
	case 0x0:
		c.B = c.sub8(c.B, get8(), 0)
	case 0x1:
		c.sub8(c.B, get8(), 0)
	case 0x2:
		c.B = c.sub8(c.B, get8(), c.CC&CC_C)
	case 0x3:
		c.SetD(c.add16(c.D(), get16()))
	case 0x4:
		c.B &= get8()
		c.logic(c.B)
	case 0x5:
		c.logic(c.B & get8())
	case 0x6:
		c.B = get8()
		c.logic(c.B)
	case 0x7:
		if imm {
			return c.illegal(pc0, op)
		}
		c.logic(c.B)
		c.wr(ea, c.B)
	case 0x8:
		c.B ^= get8()
		c.logic(c.B)
	case 0x9:
		c.B = c.add8(c.B, get8(), c.CC&CC_C)
	case 0xa:
		c.B |= get8()
		c.logic(c.B)
	case 0xb:
		c.B = c.add8(c.B, get8(), 0)
	case 0xc:
		v := get16()
		c.SetD(v)
		c.nz16(v)
		c.CC &^= CC_V
	case 0xd:
		if imm {
			return c.illegal(pc0, op)
		}
		v := c.D()
		c.nz16(v)
		c.CC &^= CC_V
		c.wr16(ea, v)
	case 0xe: // LDU / LDS
		v := get16()
		if page == 0 {
			c.U = v
		} else {
			c.S = v
		}
		c.nz16(v)
		c.CC &^= CC_V
	case 0xf: // STU / STS
		if imm {
			return c.illegal(pc0, op)
		}
		v := c.U
		if page == 1 {
			v = c.S
		}
		c.nz16(v)
		c.CC &^= CC_V
		c.wr16(ea, v)
	}
	return aluCycles(true, n, mode, page, xcy)
}

// aluCycles returns the cycle count of an 0x80-0xff instruction.
func aluCycles(bside bool, n byte, mode byte, page int, xcy int) int {
	wide16 := false
	load16 := false
	if !bside {
		switch n {
		case 0x3, 0xc:
			wide16 = true // SUBD, CMPX: slow 16-bit
		case 0xe, 0xf:
			load16 = true
		}
	} else {
		switch n {
		case 0x3:
			wide16 = true
		case 0xc, 0xd, 0xe, 0xf:
			load16 = true
		}
	}
	var cy int
	switch {
	case wide16:
		cy = [4]int{4, 6, 6, 7}[mode]
	case load16:
		cy = [4]int{3, 5, 5, 6}[mode]
	default:
		cy = [4]int{2, 4, 4, 5}[mode]
	}
	if page != 0 {
		cy++
	}
	if mode == 2 {
		cy += xcy
	}
	return cy
}
