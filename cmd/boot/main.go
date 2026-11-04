// Command boot is a development probe: it runs the main 6809 headless against
// stubbed hardware (AVG always done, mathbox never busy, sound board always
// ready) and reports which I/O regions the program touches. It exists to
// confirm the memory map before the real devices are written.
//
//	boot <roms> [frames]
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"

	"starwars/internal/assets"
	"starwars/internal/m6809"
)

type bus struct {
	rom    *assets.ROMs
	ram    [0x10000]byte
	bank   int
	reads  map[string]int
	writes map[string]int
	avgGo  int
	cpu    *m6809.CPU
}

func region(a uint16) string {
	switch {
	case a >= 0x4300 && a < 0x4320:
		return "IN0"
	case a >= 0x4320 && a < 0x4340:
		return "IN1"
	case a >= 0x4340 && a < 0x4360:
		return "DSW0"
	case a >= 0x4360 && a < 0x4380:
		return "DSW1"
	case a >= 0x4380 && a < 0x43a0:
		return "ADC data"
	case a == 0x4400:
		return "sound latch"
	case a == 0x4401:
		return "latch flags"
	case a >= 0x4500 && a < 0x4600:
		return "X2212 NVRAM"
	case a >= 0x4600 && a < 0x4620:
		return "AVG go"
	case a >= 0x4620 && a < 0x4640:
		return "AVG reset"
	case a >= 0x4640 && a < 0x4660:
		return "watchdog"
	case a >= 0x4660 && a < 0x4680:
		return "IRQ ack"
	case a >= 0x4680 && a < 0x46a0:
		return fmt.Sprintf("LS259 bit %d", a&7)
	case a >= 0x46a0 && a < 0x46c0:
		return "NVRAM store"
	case a >= 0x46c0 && a < 0x46c4:
		return "ADC start"
	case a == 0x46e0:
		return "sound reset"
	case a >= 0x4700 && a < 0x4708:
		return fmt.Sprintf("math $%04x", a)
	}
	return ""
}

func (b *bus) Read(a uint16) byte {
	if r := region(a); r != "" {
		b.reads[r]++
	}
	switch {
	case a < 0x3000:
		return b.ram[a]
	case a < 0x4000:
		return b.rom.Vector[a-0x3000]
	case a >= 0x4300 && a < 0x4320:
		return 0xff // nothing pressed (active low)
	case a >= 0x4320 && a < 0x4340:
		return 0x40 | 0x3c // VG halted, math idle, buttons up
	case a >= 0x4340 && a < 0x4360:
		return 0x88 // MAME DSW0 defaults
	case a >= 0x4360 && a < 0x4380:
		return 0x02
	case a >= 0x4380 && a < 0x43a0:
		return 0x80 // stick centred
	case a == 0x4401:
		return 0x00 // both latches empty
	case a >= 0x4500 && a < 0x4600:
		return b.ram[a] | 0xf0
	case a >= 0x4800 && a < 0x6000:
		return b.ram[a]
	case a >= 0x6000 && a < 0x8000:
		if b.bank == 1 {
			return b.rom.Main[0x10000+int(a-0x6000)]
		}
		return b.rom.Main[a]
	case a >= 0x8000:
		return b.rom.Main[a]
	}
	return 0xff
}

func (b *bus) Write(a uint16, v byte) {
	if r := region(a); r != "" {
		b.writes[r]++
	}
	switch {
	case a < 0x3000, a >= 0x4800 && a < 0x6000:
		b.ram[a] = v
	case a >= 0x4500 && a < 0x4600:
		b.ram[a] = v & 0x0f
	case a >= 0x4600 && a < 0x4620:
		b.avgGo++
	case a >= 0x4660 && a < 0x4680:
		b.cpu.IRQ = false
	case a >= 0x4680 && a < 0x46a0 && a&7 == 4:
		b.bank = int(v >> 7)
	}
}

func main() {
	rom, err := assets.Load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	frames := 600
	if len(os.Args) > 2 {
		frames, _ = strconv.Atoi(os.Args[2])
	}
	b := &bus{rom: rom, reads: map[string]int{}, writes: map[string]int{}}
	c := &m6809.CPU{Bus: b}
	b.cpu = c
	illegal := 0
	c.Illegal = func(_ *m6809.CPU, pc uint16, op byte) {
		if illegal < 5 {
			fmt.Printf("illegal op %02x at %04x\n", op, pc)
		}
		illegal++
	}
	c.Reset()
	fmt.Printf("reset vector %04x\n", c.PC)
	// 1.512 MHz CPU, IRQ every 4096*12 master clocks = 6144 CPU cycles (~246 Hz).
	const irqCycles = 6144
	const irqPerFrame = 6 // vector refresh = IRQ/6 = ~41 Hz
	hot := map[uint16]int{}
	for f := 0; f < frames; f++ {
		for i := 0; i < irqPerFrame; i++ {
			end := c.Cycles + irqCycles
			for c.Cycles < end {
				hot[c.PC&0xff00]++
				c.Step()
			}
			c.IRQ = true
		}
	}
	fmt.Printf("ran %d frames (%d cycles), AVG go writes: %d, illegal ops: %d, bank now %d\n",
		frames, c.Cycles, b.avgGo, illegal, b.bank)
	show := func(t string, m map[string]int) {
		var k []string
		for n := range m {
			k = append(k, n)
		}
		sort.Strings(k)
		fmt.Println(t)
		for _, n := range k {
			fmt.Printf("  %-14s %d\n", n, m[n])
		}
	}
	show("reads:", b.reads)
	show("writes:", b.writes)
	type kv struct {
		a uint16
		n int
	}
	var hs []kv
	for a, n := range hot {
		hs = append(hs, kv{a, n})
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].n > hs[j].n })
	fmt.Println("hottest code pages:")
	for i := 0; i < 8 && i < len(hs); i++ {
		fmt.Printf("  $%04x %d\n", hs[i].a, hs[i].n)
	}
}
