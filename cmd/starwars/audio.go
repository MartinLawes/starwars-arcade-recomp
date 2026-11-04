package main

import (
	"encoding/binary"
	"math"
	"os"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio"

	"starwars/internal/sound"
)

// audioOut streams the sound board's mono output to Ebitengine as float32
// stereo. The emulation pushes samples every tick (60 Hz, ~800 samples); the
// audio thread pulls. Read must never block: Ebitengine holds its audio lock
// while calling it, and the game loop needs that lock every frame.
//
// To ride out the 60 Hz bursts, real samples are only served once a cushion
// (audioPrime) has built up; if the queue ever runs dry the reader goes back
// to priming instead of trickling out gaps.
type audioOut struct {
	mu     sync.Mutex
	buf    []float32 // mono samples waiting to be played
	vol    float32
	primed bool
	player *audio.Player
}

const (
	audioPrime    = sound.SampleRate / 30  // ~33 ms (two ticks) before playing
	audioMaxQueue = sound.SampleRate / 4   // clocks drifting apart: trim at 250 ms...
	audioTrimTo   = sound.SampleRate / 12  // ...back to ~83 ms
	audioSilence  = sound.SampleRate / 200 // 5 ms of silence per read while priming
)

func newAudio() *audioOut {
	a := &audioOut{vol: 1}
	if os.Getenv("STARWARS_NOAUDIO") != "" {
		return a // silent (no audio device needed)
	}
	ctx := audio.NewContext(sound.SampleRate)
	p, err := ctx.NewPlayerF32(a)
	if err != nil {
		return a
	}
	p.SetBufferSize(40 * time.Millisecond)
	p.Play()
	a.player = p
	return a
}

// push queues mono samples.
func (a *audioOut) push(s []float32) {
	if len(s) == 0 {
		return
	}
	a.mu.Lock()
	a.buf = append(a.buf, s...)
	if len(a.buf) > audioMaxQueue {
		drop := len(a.buf) - audioTrimTo
		a.buf = append(a.buf[:0], a.buf[drop:]...)
	}
	a.mu.Unlock()
}

// Read implements io.Reader for the player: float32 LE stereo. Never blocks.
func (a *audioOut) Read(p []byte) (int, error) {
	frames := len(p) / 8
	if frames == 0 {
		return 0, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.primed && len(a.buf) >= audioPrime {
		a.primed = true
	}
	if !a.primed || len(a.buf) == 0 {
		a.primed = false
		n := frames
		if n > audioSilence {
			n = audioSilence
		}
		for i := 0; i < n; i++ {
			binary.LittleEndian.PutUint64(p[i*8:], 0)
		}
		return n * 8, nil
	}
	n := frames
	if n > len(a.buf) {
		n = len(a.buf)
	}
	vol := a.vol
	for i := 0; i < n; i++ {
		v := a.buf[i] * vol
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		u := math.Float32bits(v)
		binary.LittleEndian.PutUint32(p[i*8:], u)
		binary.LittleEndian.PutUint32(p[i*8+4:], u)
	}
	a.buf = append(a.buf[:0], a.buf[n:]...)
	return n * 8, nil
}

func (a *audioOut) setVolume(v float32) {
	a.mu.Lock()
	a.vol = v
	a.mu.Unlock()
}

func (a *audioOut) clear() {
	a.mu.Lock()
	a.buf = a.buf[:0]
	a.primed = false
	a.mu.Unlock()
}
