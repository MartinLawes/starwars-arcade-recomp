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
// stereo. The emulation pushes samples every tick (60 Hz); the audio thread
// pulls. Read waits for real samples rather than inventing silence, so the
// player can't run ahead of the emulation; it only pads with silence when the
// game is genuinely not producing sound (paused, menu open, a stall).
type audioOut struct {
	mu     sync.Mutex
	buf    []float32 // mono samples waiting to be played
	vol    float32
	notify chan struct{}
	player *audio.Player
}

const (
	// If more than this is queued (emulation and sound card clocks drift
	// apart), trim back to audioTrimTo. Generous so it practically never fires.
	audioMaxQueue = sound.SampleRate / 4  // 250 ms
	audioTrimTo   = sound.SampleRate / 10 // 100 ms
	// How long Read waits for the emulation before padding with silence.
	audioWait = 60 * time.Millisecond
)

func newAudio() *audioOut {
	a := &audioOut{vol: 1, notify: make(chan struct{}, 1)}
	if os.Getenv("STARWARS_NOAUDIO") != "" {
		return a // silent (no audio device needed)
	}
	ctx := audio.NewContext(sound.SampleRate)
	p, err := ctx.NewPlayerF32(a)
	if err != nil {
		return a
	}
	p.SetBufferSize(60 * time.Millisecond)
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
	select {
	case a.notify <- struct{}{}:
	default:
	}
}

// Read implements io.Reader for the player: float32 LE stereo.
func (a *audioOut) Read(p []byte) (int, error) {
	frames := len(p) / 8
	if frames == 0 {
		return 0, nil
	}
	deadline := time.Now().Add(audioWait)
	for {
		a.mu.Lock()
		have := len(a.buf)
		a.mu.Unlock()
		if have > 0 {
			break
		}
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		select {
		case <-a.notify:
		case <-time.After(left):
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	n := frames
	if n > len(a.buf) {
		n = len(a.buf)
	}
	if n == 0 {
		// Nothing coming (paused / stalled): a short stretch of silence.
		n = frames
		if n > sound.SampleRate/100 {
			n = sound.SampleRate / 100
		}
		for i := 0; i < n; i++ {
			binary.LittleEndian.PutUint64(p[i*8:], 0)
		}
		return n * 8, nil
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
	a.mu.Unlock()
}
