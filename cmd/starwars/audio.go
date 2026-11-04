package main

import (
	"encoding/binary"
	"math"
	"os"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/audio"

	"starwars/internal/sound"
)

// audioOut streams the sound board's mono output to Ebitengine as float32
// stereo. The emulation pushes samples each tick; the audio thread pulls.
type audioOut struct {
	mu     sync.Mutex
	buf    []float32 // mono samples waiting to be played
	vol    float32
	player *audio.Player
}

const (
	audioMaxQueue = sound.SampleRate / 6 // drop if more than ~160 ms is waiting
	audioPrefill  = sound.SampleRate / 40
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
	p.SetBufferSize(sound.SampleRate / 20 * 8) // ~50 ms (bytes: 4 per sample, 2 channels)
	p.Play()
	a.player = p
	return a
}

// push queues mono samples.
func (a *audioOut) push(s []float32) {
	a.mu.Lock()
	a.buf = append(a.buf, s...)
	if len(a.buf) > audioMaxQueue {
		a.buf = append(a.buf[:0], a.buf[len(a.buf)-audioPrefill:]...)
	}
	a.mu.Unlock()
}

// Read implements io.Reader for the player: float32 LE stereo.
func (a *audioOut) Read(p []byte) (int, error) {
	frames := len(p) / 8
	if frames == 0 {
		return 0, nil
	}
	a.mu.Lock()
	n := frames
	if n > len(a.buf) {
		n = len(a.buf)
	}
	vol := a.vol
	for i := 0; i < frames; i++ {
		var v float32
		if i < n {
			v = a.buf[i] * vol
		}
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
	a.mu.Unlock()
	return frames * 8, nil
}

func (a *audioOut) setVolume(v float32) {
	a.mu.Lock()
	a.vol = v
	a.mu.Unlock()
}

func (a *audioOut) clear() {
	a.mu.Lock()
	a.buf = a.buf[:0]
	a.mu.Unlock()
}
