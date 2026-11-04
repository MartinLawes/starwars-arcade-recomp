package main

import (
	"math"
	"testing"
	"time"
)

// Read is called with Ebitengine's audio lock held: it must never block.
func TestAudioReadNeverBlocks(t *testing.T) {
	a := &audioOut{vol: 1}
	buf := make([]byte, 4096*8)
	for _, fill := range []int{0, 100, audioPrime, 48000} {
		a.clear()
		a.push(make([]float32, fill))
		t0 := time.Now()
		for i := 0; i < 20; i++ {
			a.Read(buf)
		}
		if d := time.Since(t0); d > 20*time.Millisecond {
			t.Errorf("queue %d: 20 reads took %v", fill, d)
		}
	}
}

// Producer: 800 samples every 1/60 s, like the game loop. Consumer: a sound
// card pulling chunks in real time and keeping ~40 ms buffered. Once primed,
// nothing played may be silence.
func TestAudioStreamHasNoGaps(t *testing.T) {
	for _, chunk := range []int{256, 1024, 1920} {
		a := &audioOut{vol: 1}
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
		played, silentAfterStart := 0, 0
		started := false
		for time.Since(start) < 1500*time.Millisecond {
			n, _ := a.Read(buf)
			for i := 0; i < n/8; i++ {
				v := math.Float32frombits(uint32(buf[i*8]) | uint32(buf[i*8+1])<<8 | uint32(buf[i*8+2])<<16 | uint32(buf[i*8+3])<<24)
				if v != 0 {
					started = true
				} else if started {
					silentAfterStart++
				}
				played++
			}
			due := start.Add(time.Duration(played) * time.Second / 48000)
			time.Sleep(time.Until(due) - 40*time.Millisecond)
		}
		close(stop)
		t.Logf("chunk %d: played %d samples, silent after start %d", chunk, played, silentAfterStart)
		if silentAfterStart > 0 {
			t.Errorf("chunk %d: %d silent samples after playback started", chunk, silentAfterStart)
		}
	}
}
