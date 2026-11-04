// Package assets loads the Atari Star Wars (1983) ROM set at run time.
// The ROMs are not part of this repository; supply your own dump.
//
// Files are matched by CRC32, not by name, so MAME names ("136021-214.1f"),
// older dotted names ("136021.214") or anything else all work. The four
// mathbox PROMs are 1K x 4 parts: only the low nibble is real, and some dumps
// carry floating junk in the high nibble, so those are masked before checking.
package assets

import (
	"archive/zip"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ROMs holds every image the board needs, already placed in its address space.
type ROMs struct {
	// Main 6809. $6000-$7fff is banked (LS259 bit 4): bank 0 is Main[0x6000:],
	// bank 1 is Main[0x10000:]. $8000-$ffff is fixed.
	Main [0x12000]byte
	// Vector ROM, main CPU $3000-$3fff (also read by the AVG).
	Vector [0x1000]byte
	// Sound 6809, full 64K image: $4000-$7fff and mirrored at $c000-$ffff.
	Sound [0x10000]byte
	// AVG state machine PROM (136021-109).
	AVGProm [0x100]byte
	// Mathbox microcode PROMs 110..113, low nibble only, in MAME order.
	Math [0x1000]byte
}

type part struct {
	mame   string // canonical MAME name, for messages
	crc    uint32
	size   int
	nibble bool
	put    func(r *ROMs, b []byte)
}

// Star Wars revision 2 ("starwars" in MAME). Revision 1 / original sets
// differ only in 136021-114 and -103; added as alternates.
var layout = []part{
	{"136021-214.1f", 0x04f1876e, 0x4000, false, putMainBank},
	{"136021-102.1hj", 0xf725e344, 0x2000, false, func(r *ROMs, b []byte) { copy(r.Main[0x8000:], b) }},
	{"136021-203.1jk", 0xf6da0a00, 0x2000, false, func(r *ROMs, b []byte) { copy(r.Main[0xa000:], b) }},
	{"136021-104.1kl", 0x7e406703, 0x2000, false, func(r *ROMs, b []byte) { copy(r.Main[0xc000:], b) }},
	{"136021-206.1m", 0xc7e51237, 0x2000, false, func(r *ROMs, b []byte) { copy(r.Main[0xe000:], b) }},
	{"136021-105.1l", 0x538e7d2f, 0x1000, false, func(r *ROMs, b []byte) { copy(r.Vector[:], b) }},
	{"136021-107.1jk", 0xdbf3aea2, 0x2000, false, func(r *ROMs, b []byte) { copy(r.Sound[0x4000:], b); copy(r.Sound[0xc000:], b) }},
	{"136021-208.1h", 0xe38070a8, 0x2000, false, func(r *ROMs, b []byte) { copy(r.Sound[0x6000:], b); copy(r.Sound[0xe000:], b) }},
	{"136021-109.4b", 0x82fc3eb2, 0x100, false, func(r *ROMs, b []byte) { copy(r.AVGProm[:], b) }},
	{"136021-110.7h", 0x810e040e, 0x400, true, func(r *ROMs, b []byte) { copy(r.Math[0x000:], b) }},
	{"136021-111.7j", 0xae69881c, 0x400, true, func(r *ROMs, b []byte) { copy(r.Math[0x400:], b) }},
	{"136021-112.7k", 0xecf22628, 0x400, true, func(r *ROMs, b []byte) { copy(r.Math[0x800:], b) }},
	{"136021-113.7l", 0x83febfde, 0x400, true, func(r *ROMs, b []byte) { copy(r.Math[0xc00:], b) }},
}

// Alternates accepted in place of a revision-2 part (same slot).
var alternates = map[uint32]string{
	0xe75ff867: "136021-214.1f", // 136021-114 (rev 1 / original)
	0x3fde9ccb: "136021-203.1jk", // 136021-103 (original)
}

func putMainBank(r *ROMs, b []byte) {
	copy(r.Main[0x6000:], b[:0x2000])
	copy(r.Main[0x10000:], b[0x2000:])
}

// MissingError lists the ROM images that were not found.
type MissingError struct {
	Missing []string
	Sources []string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("missing ROM files: %s", strings.Join(e.Missing, ", "))
}

func collect(path string, blobs *[][]byte) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		ents, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range ents {
			p := filepath.Join(path, e.Name())
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
				_ = collect(p, blobs)
				continue
			}
			if b, err := os.ReadFile(p); err == nil && len(b) <= 0x10000 {
				*blobs = append(*blobs, b)
			}
		}
		return nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.UncompressedSize64 > 0x10000 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err == nil {
			*blobs = append(*blobs, b)
		}
	}
	return nil
}

func lowNibble(b []byte) []byte {
	o := make([]byte, len(b))
	for i, v := range b {
		o[i] = v & 0x0f
	}
	return o
}

// Load reads the ROM set from the given zips and folders.
func Load(paths ...string) (*ROMs, error) {
	var blobs [][]byte
	var srcs []string
	for _, p := range paths {
		if err := collect(p, &blobs); err == nil {
			srcs = append(srcs, p)
		}
	}
	byCRC := map[uint32][]byte{}
	for _, b := range blobs {
		byCRC[crc32.ChecksumIEEE(b)] = b
		if len(b) == 0x400 {
			m := lowNibble(b)
			byCRC[crc32.ChecksumIEEE(m)] = m
		}
	}
	for crc, slot := range alternates {
		if b, ok := byCRC[crc]; ok {
			for _, pt := range layout {
				if pt.mame == slot {
					if _, have := byCRC[pt.crc]; !have {
						byCRC[pt.crc] = b
					}
				}
			}
		}
	}
	r := &ROMs{}
	var missing []string
	for _, pt := range layout {
		b, ok := byCRC[pt.crc]
		if !ok || len(b) != pt.size {
			missing = append(missing, pt.mame)
			continue
		}
		pt.put(r, b)
	}
	if len(missing) > 0 {
		return nil, &MissingError{Missing: missing, Sources: srcs}
	}
	return r, nil
}
