package m6809

import "testing"

type ram [0x10000]byte

func (r *ram) Read(a uint16) byte     { return r[a] }
func (r *ram) Write(a uint16, v byte) { r[a] = v }

func TestLoadAddStore(t *testing.T) {
	var m ram
	// LDA #$12 ; ADDA #$30 ; STA $0200
	copy(m[0x1000:], []byte{0x86, 0x12, 0x8B, 0x30, 0xB7, 0x02, 0x00})
	m[0xfffe], m[0xffff] = 0x10, 0x00
	c := &CPU{Bus: &m}
	c.Reset()
	for i := 0; i < 3; i++ {
		c.Step()
	}
	if m[0x200] != 0x42 {
		t.Fatalf("got %#x want 0x42", m[0x200])
	}
}
