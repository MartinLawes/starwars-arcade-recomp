// Package tms5220 is the TI TMS5220 LPC speech synthesizer, ported from
// MAME's tms5220.cpp (Frank Palazzolo, Aaron Giles, Jonathan Gevaryahu,
// Raphael Nabet, Couriersud, Michael Zapf): FIFO / Speak External, frame
// parser, parameter interpolation, chirp / noise excitation and the 10-pole
// lattice filter, with the same integer widths. Only what a host CPU driving
// /WS, /RS and the data bus needs (no VSM).
//
// Time is measured in host CPU cycles. The chip makes one sample every
// CyclesPerSample host cycles.
package tms5220

const fifoSize = 16
const noCommand = 0xff

var reloadTable = [4]uint8{0, 2, 4, 6}

// TMS5220 is one chip.
type TMS5220 struct {
	// CyclesPerSample is host cycles per output sample (clock / 80).
	CyclesPerSample uint64
	// ReadyCycles / StatusCycles: how long /READY stays high after /WS / /RS
	// fall (16 chip clocks / 13 us), in host cycles.
	ReadyCycles, StatusCycles uint64

	// Out receives each output sample (16-bit range).
	Out func(s int16)

	now        uint64 // host cycle the chip has been run up to
	nextSample uint64
	readyAt    uint64
	readyArmed bool

	fifo                                     [fifoSize]uint8
	fifoHead, fifoTail, fifoCount, fifoBits  uint8
	prevTalk, SPEN, DDIS, TALK, TALKD        bool
	bufferLow, bufferEmpty, irqPin, readyPin bool
	cmdReg                                   uint8
	dataLatched                              bool
	OLDE, OLDP                               bool
	newEnergy, newPitch                      uint8
	newK                                     [10]uint8
	curEnergy, curPitch                      int16
	curK                                     [10]int16
	prevEnergy                               uint16
	subcycle, subcReload, PC, IP             uint8
	inhibit, uvZpar, zpar, pitchZero         bool
	pitchCount                               uint16
	u                                        [11]int32
	x                                        [10]int32
	RNG                                      uint16
	excitation                               int16
	ioReady                                  bool
	rsws                                     uint8
	readLatch, writeLatch                    uint8
	RDB                                      bool

	// Diagnostics.
	DbgWS, DbgRS, DbgWrites, DbgCmds int
}

// New returns a reset chip.
func New(cyclesPerSample, readyCycles, statusCycles uint64) *TMS5220 {
	t := &TMS5220{CyclesPerSample: cyclesPerSample, ReadyCycles: readyCycles, StatusCycles: statusCycles}
	t.rsws = 3
	t.Reset()
	t.ioReady = true
	t.readyPin = true
	return t
}

// Reset is the chip reset (also the RESET command).
func (t *TMS5220) Reset() {
	t.fifo = [fifoSize]uint8{}
	t.fifoHead, t.fifoTail, t.fifoCount, t.fifoBits = 0, 0, 0, 0
	t.SPEN, t.DDIS, t.TALK, t.TALKD, t.prevTalk, t.irqPin = false, false, false, false, false, false
	t.bufferEmpty, t.bufferLow = true, true
	t.cmdReg = noCommand
	t.dataLatched = false
	t.RDB = false
	t.newEnergy, t.curEnergy, t.prevEnergy = 0, 0, 0
	t.newPitch, t.curPitch = 0, 0
	t.zpar, t.uvZpar = false, false
	t.newK = [10]uint8{}
	t.curK = [10]int16{}
	t.inhibit = true
	t.subcycle, t.pitchCount, t.PC = 0, 0, 0
	t.subcReload = 1
	t.OLDE, t.OLDP = true, true
	t.IP = reloadTable[0]
	t.RNG = 0x1fff
	t.u = [11]int32{}
	t.x = [10]int32{}
}

func (t *TMS5220) talkStatus() bool { return t.SPEN || t.TALKD }

// ---- host interface ----

// Run brings the chip up to host cycle now, producing samples.
func (t *TMS5220) Run(now uint64) {
	for {
		next := t.nextSample
		if t.readyArmed && t.readyAt <= next && t.readyAt <= now {
			t.now = t.readyAt
			t.readyArmed = false
			t.setIOReady()
			continue
		}
		if next > now {
			break
		}
		t.now = next
		t.nextSample += t.CyclesPerSample
		s := t.sample()
		if t.Out != nil {
			t.Out(s)
		}
	}
	t.now = now
}

// WS sets the /WS pin (false = asserted) at host cycle now.
func (t *TMS5220) WS(state bool, now uint64) {
	t.Run(now)
	nv := t.rsws&2 | b2u8(state)
	t.pins(nv, now, t.ReadyCycles)
}

// RS sets the /RS pin at host cycle now.
func (t *TMS5220) RS(state bool, now uint64) {
	t.Run(now)
	nv := t.rsws&1 | b2u8(state)<<1
	t.pins(nv, now, t.StatusCycles)
}

func (t *TMS5220) pins(nv uint8, now, delay uint64) {
	if nv == t.rsws {
		return
	}
	falling := (t.rsws ^ nv) &^ nv
	if falling&1 != 0 {
		t.DbgWS++
	}
	if falling&2 != 0 {
		t.DbgRS++
	}
	t.rsws = nv
	switch nv {
	case 0: // illegal on a plain 5220
		return
	case 3:
		t.readLatch = 0xff
		return
	}
	if falling == 0 {
		return
	}
	t.ioReady = false
	t.readyAt = now + delay
	t.readyArmed = true
}

// Data puts a byte on the data bus (latched for the next /WS cycle).
func (t *TMS5220) Data(v byte, now uint64) {
	t.Run(now)
	t.writeLatch = v
	t.dataLatched = true
}

// Status is the data bus value during a read (/RS low).
func (t *TMS5220) Status(now uint64) byte {
	t.Run(now)
	if t.rsws == 1 {
		return t.readLatch
	}
	return 0xff
}

// ReadyQ is the /READY pin (false = ready) at host cycle now.
func (t *TMS5220) ReadyQ(now uint64) bool {
	t.Run(now)
	return !t.ioReady
}

// Talking reports talk status (for diagnostics).
func (t *TMS5220) Talking() bool { return t.talkStatus() }

func (t *TMS5220) setIOReady() {
	switch t.rsws {
	case 2: // write
		if t.fifoCount >= fifoSize && t.DDIS {
			t.readyAt = t.now + t.ReadyCycles
			t.readyArmed = true
			return
		}
		if t.cmdReg == noCommand {
			t.dataLatched = false
			t.dataWrite(t.writeLatch)
		}
	case 1:
		t.readLatch = t.statusRead(true)
		t.ioReady = true
	default:
		t.ioReady = true
	}
}

func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// ---- chip internals (MAME order) ----

func (t *TMS5220) resetFrameIdx() {
	t.newEnergy = 0
	t.newPitch = 0
	for i := 0; i < 4; i++ {
		t.newK[i] = 0
	}
	for i := 4; i < 7; i++ {
		t.newK[i] = 0xf
	}
	for i := 7; i < 10; i++ {
		t.newK[i] = 0x7
	}
}

func (t *TMS5220) dataWrite(data uint8) {
	t.DbgWrites++
	if !t.DDIS {
		t.DbgCmds++
	}
	oldLow := t.bufferLow
	if t.DDIS {
		if t.fifoCount < fifoSize {
			t.fifo[t.fifoTail] = data
			t.fifoTail = (t.fifoTail + 1) % fifoSize
			t.fifoCount++
			t.updateFifo()
			if !t.SPEN && oldLow && !t.bufferLow {
				t.zpar, t.uvZpar = true, true
				t.OLDE, t.OLDP = true, true
				t.SPEN = true
				t.TALK = true // FAST_START_HACK
				t.resetFrameIdx()
			}
		}
		t.dataLatched = false
	} else {
		t.processCommand(data)
	}
	if !t.dataLatched {
		t.ioReady = true
	}
}

func (t *TMS5220) updateFifo() {
	if t.fifoCount <= 8 {
		if !t.bufferLow {
			t.bufferLow = true
			t.irqPin = true
		}
	} else {
		t.bufferLow = false
	}
	if t.fifoCount == 0 {
		if !t.bufferEmpty {
			t.bufferEmpty = true
			t.irqPin = true
		}
		if t.DDIS {
			t.TALK, t.SPEN = false, false
		}
	} else {
		t.bufferEmpty = false
	}
	if t.prevTalk && !t.talkStatus() {
		t.irqPin = true
		t.DDIS = false
		t.prevTalk = false
		if t.cmdReg != noCommand {
			t.processCommand(t.cmdReg)
			if t.dataLatched {
				t.readyAt = t.now + t.ReadyCycles
				t.readyArmed = true
			} else {
				t.ioReady = true
			}
		}
	}
	t.prevTalk = t.talkStatus()
	if t.rsws == 1 && !t.RDB {
		t.readLatch = t.statusRead(false)
	}
}

func (t *TMS5220) readBits(count int) int {
	val := 0
	if !t.DDIS {
		return 1<<count - 1
	}
	for ; count > 0; count-- {
		val = val<<1 | int((t.fifo[t.fifoHead]>>t.fifoBits)&1)
		t.fifoBits++
		if t.fifoBits >= 8 {
			t.fifoCount--
			t.fifo[t.fifoHead] = 0
			t.fifoHead = (t.fifoHead + 1) % fifoSize
			t.fifoBits = 0
			t.updateFifo()
		}
	}
	return val
}

func (t *TMS5220) statusRead(clearInt bool) uint8 {
	if t.RDB {
		t.RDB = false
		return 0
	}
	if clearInt {
		t.irqPin = false
	}
	return b2u8(t.talkStatus())<<7 | b2u8(t.bufferLow)<<6 | b2u8(t.bufferEmpty)<<5
}

func (t *TMS5220) processCommand(cmd uint8) {
	t.cmdReg = cmd
	switch cmd & 0x70 {
	case 0x10: // read byte (no VSM)
		if !t.talkStatus() {
			t.RDB = true
			t.cmdReg = noCommand
		}
	case 0x00, 0x20: // NOP / set rate (5220C only)
		t.cmdReg = noCommand
	case 0x30: // read and branch (no VSM)
		if !t.talkStatus() {
			t.RDB = false
			t.cmdReg = noCommand
		}
	case 0x40: // load address
		if !t.talkStatus() {
			t.cmdReg = noCommand
		}
	case 0x50: // speak (VSM)
		t.SPEN = true
		t.TALK = true
		t.DDIS = false
		t.zpar, t.uvZpar, t.OLDE, t.OLDP = true, true, true, true
		t.resetFrameIdx()
		t.cmdReg = noCommand
	case 0x60: // speak external
		t.fifo = [fifoSize]uint8{}
		t.fifoHead, t.fifoTail, t.fifoCount, t.fifoBits = 0, 0, 0, 0
		t.DDIS = true
		t.zpar, t.uvZpar, t.OLDE, t.OLDP = true, true, true, true
		t.resetFrameIdx()
		t.RDB = false
		t.cmdReg = noCommand
	case 0x70: // reset
		t.Reset()
		t.cmdReg = noCommand
	}
	t.updateFifo()
}

func (t *TMS5220) parseFrame() {
	t.uvZpar, t.zpar = false, false
	t.IP = reloadTable[0]
	t.updateFifo()
	if t.DDIS && t.bufferEmpty {
		return
	}
	t.newEnergy = uint8(t.readBits(4))
	t.updateFifo()
	if t.DDIS && t.bufferEmpty {
		return
	}
	if t.newEnergy == 0 || t.newEnergy == 15 {
		return
	}
	rep := t.readBits(1)
	t.newPitch = uint8(t.readBits(6))
	t.uvZpar = t.newPitch == 0
	t.updateFifo()
	if t.DDIS && t.bufferEmpty {
		return
	}
	if rep != 0 {
		return
	}
	for i := 0; i < 4; i++ {
		t.newK[i] = uint8(t.readBits(kBits[i]))
		t.updateFifo()
		if t.DDIS && t.bufferEmpty {
			return
		}
	}
	if t.newPitch == 0 {
		return
	}
	for i := 4; i < 10; i++ {
		t.newK[i] = uint8(t.readBits(kBits[i]))
		t.updateFifo()
		if t.DDIS && t.bufferEmpty {
			return
		}
	}
}

func matMul(a, b int32) int32 {
	for a > 511 {
		a -= 1024
	}
	for a < -512 {
		a += 1024
	}
	for b > 16383 {
		b -= 32768
	}
	for b < -16384 {
		b += 32768
	}
	return (a * b) >> 9
}

func (t *TMS5220) lattice() int32 {
	k := &t.curK
	u, x := &t.u, &t.x
	u[10] = matMul(int32(t.prevEnergy), int32(t.excitation)<<6)
	for i := 9; i >= 0; i-- {
		u[i] = u[i+1] - matMul(int32(k[i]), x[i])
	}
	for i := 9; i >= 1; i-- {
		x[i] = x[i-1] + matMul(int32(k[i-1]), u[i-1])
	}
	x[0] = u[0]
	t.prevEnergy = uint16(t.curEnergy)
	return u[0]
}

func clipAnalog(c int32) int16 {
	if c > 2047 {
		c = 2047
	} else if c < -2048 {
		c = -2048
	}
	c &^= 0xf
	return int16(c<<4 | (c&0x7f0)>>3 | (c&0x400)>>10)
}

func interp(cur int16, target int32, inhibit bool, shift int8, zero bool) int16 {
	v := int32(cur) + (((target - int32(cur)) * (1 - b2i(inhibit))) >> uint(shift))
	return int16(v * (1 - b2i(zero)))
}

func (t *TMS5220) sample() int16 {
	if !t.TALKD {
		t.subcycle++
		if t.subcycle == 2 && t.PC == 12 {
			if t.IP == 7 {
				t.TALKD = t.TALK
				t.updateFifo()
				if !t.TALK && t.SPEN {
					t.TALK = true
				}
			}
			t.subcycle = t.subcReload
			t.PC = 0
			t.IP = (t.IP + 1) & 7
		} else if t.subcycle == 3 {
			t.subcycle = t.subcReload
			t.PC++
		}
		return -1
	}

	if t.IP == 0 && t.PC == 12 && t.subcycle == 1 {
		t.IP = reloadTable[0]
		t.parseFrame()
		if t.newEnergy == 0x0f {
			t.TALK, t.SPEN = false, false
			t.updateFifo()
		}
		newUV := t.newPitch == 0
		newSil := t.newEnergy == 0
		t.inhibit = (!t.OLDP && newUV) || (t.OLDP && !newUV) || (t.OLDE && !newSil) || (t.OLDP && newSil)
	} else {
		inh := t.inhibit && t.IP != 0
		sh := interpCoeff[t.IP]
		if t.subcycle == 2 {
			switch {
			case t.PC == 0:
				if t.IP == 0 {
					t.pitchZero = false
				}
				t.curEnergy = interp(t.curEnergy, int32(energyTable[t.newEnergy]), inh, sh, t.zpar)
			case t.PC == 1:
				t.curPitch = interp(t.curPitch, int32(pitchTable[t.newPitch]), inh, sh, t.zpar)
			case t.PC >= 2 && t.PC <= 11:
				i := t.PC - 2
				z := t.zpar
				if i >= 4 {
					z = t.uvZpar
				}
				t.curK[i] = interp(t.curK[i], int32(kTable[i][t.newK[i]]), inh, sh, z)
			}
		}
	}

	if t.OLDP {
		if t.RNG&1 != 0 {
			t.excitation = ^0x3f
		} else {
			t.excitation = 0x40
		}
	} else if t.pitchCount >= 51 {
		t.excitation = int16(int8(chirpTable[51]))
	} else {
		t.excitation = int16(int8(chirpTable[t.pitchCount]))
	}
	for i := 0; i < 20; i++ {
		bit := (t.RNG>>12)&1 ^ (t.RNG>>3)&1 ^ (t.RNG>>2)&1 ^ t.RNG&1
		t.RNG = t.RNG<<1 | bit
	}
	s := t.lattice()
	for s > 16383 {
		s -= 32768
	}
	for s < -16384 {
		s += 32768
	}
	out := clipAnalog(s)

	t.subcycle++
	if t.subcycle == 2 && t.PC == 12 {
		if t.IP == 7 && t.inhibit {
			t.pitchZero = true
		}
		if t.IP == 7 {
			t.OLDE = t.newEnergy == 0
			t.OLDP = t.newPitch == 0
			t.TALKD = t.TALK
			t.updateFifo()
			if !t.TALK && t.SPEN {
				t.TALK = true
			}
		}
		t.subcycle = t.subcReload
		t.PC = 0
		t.IP = (t.IP + 1) & 7
	} else if t.subcycle == 3 {
		t.subcycle = t.subcReload
		t.PC++
	}
	t.pitchCount++
	if int32(t.pitchCount) >= int32(t.curPitch) || t.pitchZero {
		t.pitchCount = 0
	}
	t.pitchCount &= 0x1ff
	return out
}
