// Command shot runs Star Wars headless and writes vector-rendered PNGs.
//
//	shot <roms> <outdir> [-w 1600] [-h 1200] [-clean] t1 t2 ...   (times in seconds)
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"starwars/internal/assets"
	"starwars/internal/avg"
	"starwars/internal/board"
	"starwars/internal/vecrender"
)

func main() {
	w := flag.Int("w", 1600, "width")
	h := flag.Int("h", 1200, "height")
	clean := flag.Bool("clean", false, "clean (no glow) look")
	flag.Parse()
	args := flag.Args()
	rom, err := assets.Load(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := args[1]
	os.MkdirAll(out, 0o755)
	var times []float64
	for _, a := range args[2:] {
		t, _ := strconv.ParseFloat(a, 64)
		times = append(times, t)
	}
	sort.Float64s(times)
	p := vecrender.Defaults()
	if *clean {
		p = vecrender.Clean()
	}
	b := board.New(rom)
	var lists [][]avg.Point
	b.AVG.Frame = func(pts []avg.Point) {
		c := append([]avg.Point(nil), pts...)
		lists = append(lists, c)
		if len(lists) > 8 {
			lists = lists[1:]
		}
	}
	b.Reset()
	var segs []avg.Segment
	for i, t := range times {
		target := uint64(t * board.CPUHz)
		if target > b.CPU.Cycles {
			b.RunCycles(target - b.CPU.Cycles)
		}
		r := vecrender.NewSoft(*w, *h, p)
		var img = r.Draw(nil)
		start := len(lists) - 4
		if start < 0 {
			start = 0
		}
		nseg := 0
		for _, l := range lists[start:] {
			segs = avg.Segments(l, segs)
			nseg = len(segs)
			img = r.Draw(segs)
		}
		name := filepath.Join(out, fmt.Sprintf("shot%02d_%05.1fs.png", i, t))
		f, _ := os.Create(name)
		png.Encode(f, img)
		f.Close()
		fmt.Printf("%s  lists so far %d, segments %d, mathbox steps %d\n", name, b.AVG.Lists, nseg, b.Math.Steps)
	}
}
