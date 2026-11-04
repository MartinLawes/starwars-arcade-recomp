package main

import (
	"math"
	"testing"
	"time"
)

// Producer: 800 samples (non-zero) every 1/60 s, like the game loop.
// Consumer: pulls chunks in real time like a sound card. No audio played
// should be silence once the stream has started.
func TestAudioStreamHasNoGaps(t *testing.T) {
	for _, chunk := range []int{256, 1024, 4096} {
		a := &audioOut{vol: 1, notify: make(chan struct{}, 1)}
		stop := make(chan struct{})
		go func() {
			s := make([]float32, 800)
			for i := range s {
				s[i] = 0.5
			}
			tk := time.NewTicker(time.Second / 60)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					a.push(s)
				}
			}
		}()
		buf := make([]byte, chunk*8)
		start := time.Now()
		played, silent := 0, 0
		time.Sleep(30 * time.Millisecond)
		for time.Since(start) < 1500*time.Millisecond {
			n, _ := a.Read(buf)
			for i := 0; i < n/8; i++ {
				v := math.Float32frombits(uint32(buf[i*8]) | uint32(buf[i*8+1])<<8 | uint32(buf[i*8+2])<<16 | uint32(buf[i*8+3])<<24)
				if v == 0 {
					silent++
				}
				played++
			}
			// sound card consumes in real time
			due := start.Add(time.Duration(played) * time.Second / 48000)
			time.Sleep(time.Until(due) - 40*time.Millisecond) // keep ~40 ms buffered, like a device
		}
		close(stop)
		t.Logf("chunk %d: played %d samples, silent %d", chunk, played, silent)
		if silent > 0 {
			t.Errorf("chunk %d: %d silent samples inserted", chunk, silent)
		}
	}
}
