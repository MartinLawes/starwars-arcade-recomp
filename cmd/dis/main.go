// Command dis disassembles a Star Wars CPU program.
//
//	dis <rom zip|dir> main|sound <start> <end> [bank]
//
// For main, bank selects which page appears at $6000-$7fff (0 or 1).
package main

import (
	"fmt"
	"os"
	"strconv"

	"starwars/internal/assets"
	"starwars/internal/m6809"
)

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: dis <roms> main|sound <start> <end> [bank]")
		os.Exit(2)
	}
	rom, err := assets.Load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	which := os.Args[2]
	s, _ := strconv.ParseUint(os.Args[3], 16, 16)
	e, _ := strconv.ParseUint(os.Args[4], 16, 17)
	bank := 0
	if len(os.Args) > 5 {
		bank, _ = strconv.Atoi(os.Args[5])
	}
	read := func(a uint16) byte {
		if which == "sound" {
			return rom.Sound[a]
		}
		switch {
		case a >= 0x3000 && a < 0x4000:
			return rom.Vector[a-0x3000]
		case a >= 0x6000 && a < 0x8000 && bank == 1:
			return rom.Main[0x10000+int(a-0x6000)]
		}
		return rom.Main[a]
	}
	for at := int(s); at < int(e); {
		pc := uint16(at)
		t, n := m6809.Disasm(read, pc)
		raw := ""
		for i := 0; i < n; i++ {
			raw += fmt.Sprintf("%02x ", read(pc+uint16(i)))
		}
		fmt.Printf("%04x  %-15s %s\n", pc, raw, t)
		at += n
	}
}
