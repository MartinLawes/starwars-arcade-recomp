// Command starwars runs Atari's 1983 Star Wars as a native program.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"starwars/internal/assets"
	"starwars/internal/avg"
	"starwars/internal/board"
	"starwars/internal/vecrender/gpu"
)

const tps = 60

type game struct {
	b   *board.Board
	gpu *gpu.GPU
	set Settings

	menu      menu
	toast     string
	toastTime int

	list     []avg.Point
	listSeq  int
	drawnSeq int
	segs     []avg.Segment
	strokes  []gpu.Stroke // game picture, tube pixels
	frame    []gpu.Stroke // what is drawn this frame (game + overlays)

	pad      padTracker
	coinHold int
	yx, yy   float64 // yoke -1..1
	lastMX   int
	lastMY   int
	mouseOn  bool
	captured bool

	audio   *audioOut
	samples []float32

	showFPS bool
	ticks   int

	nvPath  string
	nvTimer int
	err     string
	quit    bool
}

func romSearch() []string {
	var p []string
	if len(os.Args) > 1 {
		p = append(p, os.Args[1])
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		p = append(p, filepath.Join(d, "starwars.zip"), filepath.Join(d, "roms", "starwars.zip"), filepath.Join(d, "roms"))
	}
	p = append(p, "starwars.zip", filepath.Join("roms", "starwars.zip"))
	if h, err := os.UserHomeDir(); err == nil {
		p = append(p, filepath.Join(h, "starwars.zip"))
	}
	return p
}

func main() {
	g := &game{set: loadSettings()}
	rom, err := assets.Load(romSearch()...)
	if err != nil {
		var me *assets.MissingError
		if errors.As(err, &me) {
			g.err = "Star Wars ROMs not found or incomplete.\n\nPut your starwars.zip (MAME 'starwars' set) next to this program.\n\nMissing:\n  " +
				strings.Join(me.Missing, "\n  ")
		} else {
			g.err = err.Error()
		}
	} else {
		g.b = board.New(rom)
		if cfg, err := os.UserConfigDir(); err == nil {
			dir := filepath.Join(cfg, "starwars")
			os.MkdirAll(dir, 0o755)
			g.nvPath = filepath.Join(dir, "nvram.bin")
			if nv, err := os.ReadFile(g.nvPath); err == nil && len(nv) == len(g.b.NVRAM) {
				copy(g.b.NVRAM[:], nv)
			}
		}
		g.b.AVG.Frame = func(pts []avg.Point) {
			g.list = append(g.list[:0], pts...)
			g.listSeq++
		}
		g.b.Reset()
	}
	if g.gpu, err = gpu.New(g.set.Params()); err != nil {
		panic(err)
	}
	g.buildMenu()
	g.audio = newAudio()
	g.applySettings()
	ebiten.SetWindowTitle("Star Wars")
	ebiten.SetWindowSize(winSize())
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetFullscreen(g.set.Fullscreen)
	ebiten.SetTPS(tps)
	if err := ebiten.RunGame(g); err != nil && err != ebiten.Termination {
		panic(err)
	}
	g.saveNV()
}

func (g *game) saveNV() {
	if g.b == nil || g.nvPath == "" {
		return
	}
	os.WriteFile(g.nvPath, g.b.NVRAM[:], 0o644)
	g.b.NVDirty = false
}

// gameRect is the 4:3 tube area inside the window, in device pixels.
func gameRect(w, h int) (x, y, gw, gh int) {
	gw, gh = w, w*3/4
	if gh > h {
		gh, gw = h, h*4/3
	}
	return (w - gw) / 2, (h - gh) / 2, gw, gh
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func (g *game) say(s string) {
	g.toast = s
	g.toastTime = tps * 2
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// setCapture captures (hides and frees) the mouse while playing.
func (g *game) setCapture(on bool) {
	if on == g.captured {
		return
	}
	g.captured = on
	if on {
		ebiten.SetCursorMode(ebiten.CursorModeCaptured)
	} else {
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
	}
	g.lastMX, g.lastMY = ebiten.CursorPosition()
}

func (g *game) Update() error {
	if g.quit {
		return ebiten.Termination
	}
	g.ticks++
	if g.toastTime > 0 {
		g.toastTime--
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF11) || (inpututil.IsKeyJustPressed(ebiten.KeyEnter) && ebiten.IsKeyPressed(ebiten.KeyAlt)) {
		g.set.Fullscreen = !g.set.Fullscreen
		g.applySettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF3) {
		g.showFPS = !g.showFPS
	}
	if os.Getenv("STARWARS_DEBUG") != "" && g.ticks%120 == 0 && g.b != nil {
		fmt.Printf("tick %d  game time %.1fs  TPS %.1f  FPS %.1f  lists %d\n", g.ticks, float64(g.b.CPU.Cycles)/board.CPUHz, ebiten.ActualTPS(), ebiten.ActualFPS(), g.b.AVG.Lists)
	}
	ps := g.pad.poll()
	if ps.quit {
		g.quit = true
	}

	// Settings menu: the game is paused while it is open.
	if g.menu.open {
		g.setCapture(false)
		g.menuInput(ps)
		return nil
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyTab) || ps.menuEdge {
		g.menu.open = true
		g.menu.sel = 0
		g.audio.clear()
		return nil
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF2) {
		g.cycleLook()
		g.applySettings()
		g.say("LOOK: " + g.set.look())
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyI) {
		g.set.InvertY = !g.set.InvertY
		g.applySettings()
		g.say("INVERT Y: " + onOff(g.set.InvertY))
	}
	if g.b == nil {
		return nil
	}
	in := &g.b.In

	// The mouse is captured while playing (no desktop pointer over the game's
	// own gunsight); its movement pushes a virtual yoke that holds position
	// like the real one. The menu or losing focus releases it.
	g.setCapture(ebiten.IsFocused())
	mx, my := ebiten.CursorPosition()
	dx, dy := mx-g.lastMX, my-g.lastMY
	g.lastMX, g.lastMY = mx, my
	if g.captured && (dx != 0 || dy != 0) {
		g.mouseOn = true
		// Full yoke travel (centre to edge) for ~250 px of movement at 100%.
		k := float64(g.set.MouseSpeed) / 100 / 250
		g.yx = clamp(g.yx+float64(dx)*k, -1, 1)
		g.yy = clamp(g.yy+float64(dy)*k, -1, 1)
	}
	if ps.stickActive {
		g.mouseOn = false
		g.yx, g.yy = ps.sx, ps.sy
	}
	kx, ky := 0.0, 0.0
	if ebiten.IsKeyPressed(ebiten.KeyLeft) || ebiten.IsKeyPressed(ebiten.KeyA) {
		kx--
	}
	if ebiten.IsKeyPressed(ebiten.KeyRight) || ebiten.IsKeyPressed(ebiten.KeyD) {
		kx++
	}
	if ebiten.IsKeyPressed(ebiten.KeyUp) || ebiten.IsKeyPressed(ebiten.KeyW) {
		ky--
	}
	if ebiten.IsKeyPressed(ebiten.KeyDown) || ebiten.IsKeyPressed(ebiten.KeyS) {
		ky++
	}
	if kx != 0 || ky != 0 {
		g.mouseOn = false
		g.yx = clamp(g.yx+kx*0.05, -1, 1)
		g.yy = clamp(g.yy+ky*0.05, -1, 1)
	} else if !g.mouseOn && !ps.stickActive {
		g.yx *= 0.85 // keys released: yoke springs back to centre
		g.yy *= 0.85
	}
	yy := g.yy
	if g.set.InvertY {
		yy = -yy
	}
	in.YokeX = uint8(clamp(128+g.yx*127, 0, 255))
	in.YokeY = uint8(clamp(128-yy*127, 0, 255))

	mb := func(b ebiten.MouseButton) bool { return g.captured && ebiten.IsMouseButtonPressed(b) }
	k := ebiten.IsKeyPressed
	in.Fire1 = k(ebiten.KeyZ) || k(ebiten.KeySpace) || k(ebiten.KeyControlLeft) || mb(ebiten.MouseButtonLeft) || ps.fire1 || ps.start
	in.Fire2 = k(ebiten.KeyX) || k(ebiten.KeyAltLeft) || mb(ebiten.MouseButtonRight) || ps.fire2
	in.Fire3 = k(ebiten.KeyC) || ps.fire3
	in.Fire4 = k(ebiten.KeyV) || mb(ebiten.MouseButtonMiddle) || ps.fire4
	if inpututil.IsKeyJustPressed(ebiten.Key5) || inpututil.IsKeyJustPressed(ebiten.Key6) || ps.coin {
		g.coinHold = 6
	}
	in.Coin1 = g.coinHold > 0
	if g.coinHold > 0 {
		g.coinHold--
	}
	in.Test = k(ebiten.KeyF1)
	in.Service = k(ebiten.Key9)

	g.b.RunCycles(board.CPUHz / tps)
	g.samples = g.b.Snd.TakeSamples(g.samples[:0])
	g.audio.push(g.samples)

	if g.b.NVDirty {
		g.nvTimer++
		if g.nvTimer > tps*2 {
			g.saveNV()
			g.nvTimer = 0
		}
	}
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	if g.err != "" {
		ebitenutil.DebugPrintAt(screen, g.err, 20, 20)
		return
	}
	w, h := screen.Bounds().Dx(), screen.Bounds().Dy()
	x, y, gw, gh := gameRect(w, h)
	fgw, fgh := float32(gw), float32(gh)
	newFrame := g.listSeq != g.drawnSeq
	if newFrame {
		g.drawnSeq = g.listSeq
		g.segs = avg.Segments(g.list, g.segs)
		g.strokes = g.strokes[:0]
		sx, sy := fgw/avg.Width, fgh/avg.Height
		for _, s := range g.segs {
			g.strokes = append(g.strokes, gpu.Stroke{
				X0: s.X0 * sx, Y0: s.Y0 * sy, X1: s.X1 * sx, Y1: s.Y1 * sy, R: s.R, G: s.G, B: s.B,
			})
		}
	}
	overlay := g.menu.open || g.toastTime > 0
	if !overlay {
		g.gpu.Draw(screen, float64(x), float64(y), gw, gh, g.strokes, newFrame)
	} else {
		// Overlays are redrawn every host frame; trails are suspended so the
		// static picture doesn't pile up.
		g.frame = g.frame[:0]
		dim := float32(1)
		if g.menu.open {
			dim = 0.15 // paused game behind the menu (still shows the brightness being set)
		}
		for _, s := range g.strokes {
			s.R, s.G, s.B = s.R*dim, s.G*dim, s.B*dim
			g.frame = append(g.frame, s)
		}
		if g.menu.open {
			g.frame = g.menuStrokes(g.frame, fgw, fgh)
		} else {
			th := fgh / 40
			g.frame = vecText(g.frame, g.toast, (fgw-textWidth(g.toast, th))/2, fgh*0.12, th, 1, 0.95, 0.1)
		}
		p := g.gpu.P
		g.gpu.P.Persistence = 0
		g.gpu.Draw(screen, float64(x), float64(y), gw, gh, g.frame, true)
		g.gpu.P = p
	}
	if g.showFPS {
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("FPS %.0f  TPS %.0f  strokes %d", ebiten.ActualFPS(), ebiten.ActualTPS(), len(g.strokes)), x+10, y+gh-20)
	}
}

func (g *game) Layout(ow, oh int) (int, int) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return int(float64(ow) * s), int(float64(oh) * s)
}

// Window size, overridable for testing with STARWARS_WIN=WxH.
func winSize() (int, int) {
	var w, h int
	if _, err := fmt.Sscanf(os.Getenv("STARWARS_WIN"), "%dx%d", &w, &h); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return 1280, 960
}
