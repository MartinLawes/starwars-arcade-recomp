package m6809

import "fmt"

var rmwNames = [16]string{"NEG", "", "", "COM", "LSR", "", "ROR", "ASR", "ASL", "ROL", "DEC", "", "INC", "TST", "JMP", "CLR"}
var brNames = [16]string{"BRA", "BRN", "BHI", "BLS", "BCC", "BCS", "BNE", "BEQ", "BVC", "BVS", "BPL", "BMI", "BGE", "BLT", "BGT", "BLE"}
var aNames = [16]string{"SUBA", "CMPA", "SBCA", "SUBD", "ANDA", "BITA", "LDA", "STA", "EORA", "ADCA", "ORA", "ADDA", "CMPX", "JSR", "LDX", "STX"}
var bNames = [16]string{"SUBB", "CMPB", "SBCB", "ADDD", "ANDB", "BITB", "LDB", "STB", "EORB", "ADCB", "ORB", "ADDB", "LDD", "STD", "LDU", "STU"}
var regNames = [16]string{"D", "X", "Y", "U", "S", "PC", "?", "?", "A", "B", "CC", "DP", "?", "?", "?", "?"}

// Disasm decodes one instruction. read supplies memory; it returns the text
// and the instruction length in bytes.
func Disasm(read func(uint16) byte, pc uint16) (string, int) {
	p := pc
	next := func() byte { v := read(p); p++; return v }
	next16 := func() uint16 { h := next(); return uint16(h)<<8 | uint16(next()) }
	op := next()
	page := 0
	if op == 0x10 || op == 0x11 {
		page = int(op&1) + 1
		op = next()
	}
	idx := func() string {
		pb := next()
		rn := [4]string{"X", "Y", "U", "S"}[(pb>>5)&3]
		if pb&0x80 == 0 {
			return fmt.Sprintf("%d,%s", int(int8(pb<<3))>>3, rn)
		}
		ind := pb&0x10 != 0
		var s string
		switch pb & 0xf {
		case 0:
			s = "," + rn + "+"
		case 1:
			s = "," + rn + "++"
		case 2:
			s = ",-" + rn
		case 3:
			s = ",--" + rn
		case 4:
			s = "," + rn
		case 5:
			s = "B," + rn
		case 6:
			s = "A," + rn
		case 8:
			s = fmt.Sprintf("%d,%s", int8(next()), rn)
		case 9:
			s = fmt.Sprintf("$%04x,%s", next16(), rn)
		case 0xb:
			s = "D," + rn
		case 0xc:
			s = fmt.Sprintf("%d,PC", int8(next()))
		case 0xd:
			s = fmt.Sprintf("$%04x,PC", next16())
		case 0xf:
			s = fmt.Sprintf("$%04x", next16())
		default:
			s = "?"
		}
		if ind {
			s = "[" + s + "]"
		}
		return s
	}
	fin := func(t string) (string, int) { return t, int(p - pc) }
	pref := [3]string{"", "", ""}
	_ = pref
	switch {
	case op < 0x10:
		n := rmwNames[op]
		if n == "" {
			return fin(fmt.Sprintf("?%02x", op))
		}
		return fin(fmt.Sprintf("%s <$%02x", n, next()))
	case op < 0x20:
		switch op {
		case 0x12:
			return fin("NOP")
		case 0x13:
			return fin("SYNC")
		case 0x16:
			o := next16()
			return fin(fmt.Sprintf("LBRA $%04x", p+o))
		case 0x17:
			o := next16()
			return fin(fmt.Sprintf("LBSR $%04x", p+o))
		case 0x19:
			return fin("DAA")
		case 0x1a:
			return fin(fmt.Sprintf("ORCC #$%02x", next()))
		case 0x1c:
			return fin(fmt.Sprintf("ANDCC #$%02x", next()))
		case 0x1d:
			return fin("SEX")
		case 0x1e, 0x1f:
			b := next()
			n := "EXG"
			if op == 0x1f {
				n = "TFR"
			}
			return fin(fmt.Sprintf("%s %s,%s", n, regNames[b>>4], regNames[b&15]))
		}
		return fin(fmt.Sprintf("?%02x", op))
	case op < 0x30:
		if page == 1 {
			o := next16()
			return fin(fmt.Sprintf("L%s $%04x", brNames[op&15], p+o))
		}
		o := int8(next())
		return fin(fmt.Sprintf("%s $%04x", brNames[op&15], p+uint16(int16(o))))
	case op < 0x40:
		switch op {
		case 0x30, 0x31, 0x32, 0x33:
			return fin(fmt.Sprintf("LEA%s %s", []string{"X", "Y", "S", "U"}[op&3], idx()))
		case 0x34, 0x35, 0x36, 0x37:
			m := next()
			names := []string{"CC", "A", "B", "DP", "X", "Y", "U/S", "PC"}
			s := ""
			for i := 0; i < 8; i++ {
				if m&(1<<i) != 0 {
					if s != "" {
						s += ","
					}
					s += names[i]
				}
			}
			return fin(fmt.Sprintf("%s %s", []string{"PSHS", "PULS", "PSHU", "PULU"}[op-0x34], s))
		case 0x39:
			return fin("RTS")
		case 0x3a:
			return fin("ABX")
		case 0x3b:
			return fin("RTI")
		case 0x3c:
			return fin(fmt.Sprintf("CWAI #$%02x", next()))
		case 0x3d:
			return fin("MUL")
		case 0x3f:
			return fin([]string{"SWI", "SWI2", "SWI3"}[page])
		}
		return fin(fmt.Sprintf("?%02x", op))
	case op < 0x60:
		n := rmwNames[op&15]
		if n == "" || n == "JMP" {
			return fin(fmt.Sprintf("?%02x", op))
		}
		r := "A"
		if op >= 0x50 {
			r = "B"
		}
		return fin(n + r)
	case op < 0x70:
		return fin(fmt.Sprintf("%s %s", rmwNames[op&15], idx()))
	case op < 0x80:
		return fin(fmt.Sprintf("%s $%04x", rmwNames[op&15], next16()))
	}
	names := aNames
	if op >= 0xc0 {
		names = bNames
	}
	n := names[op&15]
	switch page {
	case 1:
		switch n {
		case "CMPX":
			n = "CMPY"
		case "SUBD":
			n = "CMPD"
		case "LDX":
			n = "LDY"
		case "STX":
			n = "STY"
		case "LDU":
			n = "LDS"
		case "STU":
			n = "STS"
		}
	case 2:
		switch n {
		case "CMPX":
			n = "CMPS"
		case "SUBD":
			n = "CMPU"
		}
	}
	wide := n == "CMPX" || n == "CMPY" || n == "CMPS" || n == "CMPU" || n == "CMPD" || n == "SUBD" || n == "ADDD" ||
		n == "LDX" || n == "LDY" || n == "LDU" || n == "LDS" || n == "LDD" || n == "STX" || n == "STY" || n == "STU" || n == "STS" || n == "STD"
	switch (op >> 4) & 3 {
	case 0:
		if n == "JSR" {
			o := int8(next())
			return fin(fmt.Sprintf("BSR $%04x", p+uint16(int16(o))))
		}
		if wide {
			return fin(fmt.Sprintf("%s #$%04x", n, next16()))
		}
		return fin(fmt.Sprintf("%s #$%02x", n, next()))
	case 1:
		return fin(fmt.Sprintf("%s <$%02x", n, next()))
	case 2:
		return fin(fmt.Sprintf("%s %s", n, idx()))
	}
	return fin(fmt.Sprintf("%s $%04x", n, next16()))
}
