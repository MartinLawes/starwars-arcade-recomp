// Command wav is a development tool: it runs Star Wars headless and writes
// the sound board's output to a WAV file.
//
//	wav <roms> out.wav seconds [coinAt] [startAt]
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"

	"starwars/internal/assets"
	"starwars/internal/board"
	"starwars/internal/m6809"
	"starwars/internal/sound"
)

func main() {
	rom, err := assets.Load(os.Args[1])
	if err != nil {
		panic(err)
	}
	secs, _ := strconv.ParseFloat(os.Args[3], 64)
	coinAt, startAt := -1.0, -1.0
	if len(os.Args) > 4 {
		coinAt, _ = strconv.ParseFloat(os.Args[4], 64)
	}
	if len(os.Args) > 5 {
		startAt, _ = strconv.ParseFloat(os.Args[5], 64)
	}
	b := board.New(rom)
	ill := 0
	b.Snd.CPU.Illegal = func(_ *m6809.CPU, pc uint16, op byte) {
		if ill < 5 {
			fmt.Printf("sound CPU illegal op %02x at %04x\n", op, pc)
		}
		ill++
	}
	cmds := map[byte]int{}
	b.Snd.CmdLog = func(v byte) { cmds[v]++ }
	if os.Getenv("SPEECH_ONLY") != "" {
		b.Snd.SFXGain = 0
	}
	b.Reset()
	var pcm []float32
	talk := 0
	step := board.CPUHz / 60
	for f := 0; f < int(secs*60); f++ {
		t := float64(f) / 60
		b.In.Coin1 = coinAt >= 0 && t >= coinAt && t < coinAt+0.1
		b.In.Fire1 = startAt >= 0 && t >= startAt && t < startAt+0.2
		b.RunCycles(uint64(step))
		pcm = b.Snd.TakeSamples(pcm)
		if b.Snd.TMS.Talking() {
			talk++
		}
		if f%300 == 0 {
			var sum float64
			n := 0
			for _, v := range pcm[max(0, len(pcm)-48000*5):] {
				sum += float64(v * v)
				n++
			}
			fmt.Printf("t=%5.1fs  rms(last 5s)=%.4f  speech frames so far=%d  sound pc=%04x  ws=%d rs=%d writes=%d cmds=%d\n", t, math.Sqrt(sum/float64(max(n, 1))), talk, b.Snd.CPU.PC, b.Snd.TMS.DbgWS, b.Snd.TMS.DbgRS, b.Snd.TMS.DbgWrites, b.Snd.TMS.DbgCmds)
		}
	}
	f, _ := os.Create(os.Args[2])
	defer f.Close()
	n := len(pcm)
	hdr := []any{[]byte("RIFF"), uint32(36 + n*2), []byte("WAVEfmt "), uint32(16), uint16(1), uint16(1),
		uint32(sound.SampleRate), uint32(sound.SampleRate * 2), uint16(2), uint16(16), []byte("data"), uint32(n * 2)}
	for _, h := range hdr {
		binary.Write(f, binary.LittleEndian, h)
	}
	buf := make([]int16, n)
	for i, v := range pcm {
		buf[i] = int16(math.Max(-1, math.Min(1, float64(v))) * 32000)
	}
	binary.Write(f, binary.LittleEndian, buf)
	fmt.Println("wrote", n, "samples")
	fmt.Println("commands:", cmds)
}
