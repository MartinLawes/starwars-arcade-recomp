package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"starwars/internal/vecrender"
	"starwars/internal/vecrender/gpu"
)

// Settings are the user-adjustable options, saved as settings.json in the
// user config folder. Levels are percentages of the shipped look.
type Settings struct {
	Brightness int  `json:"brightness"` // beam energy, 10..200
	Glow       int  `json:"glow"`       // bloom halo, 0 (off)..200
	Trails     int  `json:"trails"`     // phosphor persistence, 0 (off)..300
	Dwell      int  `json:"dwell"`      // extra brightness at line ends and dots, 0 (off)..200
	Width      int  `json:"width"`      // beam width, 50..200
	InvertY    bool `json:"invert_y"`
	Fullscreen bool `json:"fullscreen"`
}

func defaultSettings() Settings {
	return Settings{Brightness: 100, Glow: 100, Trails: 100, Dwell: 100, Width: 100}
}

// cleanSettings is the sharp-lines look: no glow, no trails, light dwell.
func cleanSettings(s Settings) Settings {
	s.Glow, s.Trails, s.Dwell, s.Width = 0, 0, 50, 70
	return s
}

func (s Settings) look() string {
	d := defaultSettings()
	switch {
	case s.Brightness == d.Brightness && s.Glow == d.Glow && s.Trails == d.Trails && s.Dwell == d.Dwell && s.Width == d.Width:
		return "VECTOR MONITOR"
	case s == cleanSettings(s) && s.Brightness == d.Brightness:
		return "CLEAN LINES"
	}
	return "CUSTOM"
}

// Params turns settings into renderer parameters.
func (s Settings) Params() vecrender.Params {
	p := vecrender.Defaults()
	p.Gain *= float64(s.Brightness) / 100
	p.Bloom *= float64(s.Glow) / 100
	p.Persistence *= float64(s.Trails) / 100
	p.DotGain *= float64(s.Dwell) / 100
	p.BeamWidth *= float64(s.Width) / 100
	return p
}

func (s *Settings) clamp() {
	c := func(v *int, lo, hi int) {
		if *v < lo {
			*v = lo
		}
		if *v > hi {
			*v = hi
		}
	}
	c(&s.Brightness, 10, 200)
	c(&s.Glow, 0, 200)
	c(&s.Trails, 0, 300)
	c(&s.Dwell, 0, 200)
	c(&s.Width, 50, 200)
}

func settingsPath() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfg, "starwars", "settings.json")
}

func loadSettings() Settings {
	s := defaultSettings()
	if p := settingsPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &s)
		}
	}
	s.clamp()
	return s
}

func (s Settings) save() {
	p := settingsPath()
	if p == "" {
		return
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(p, b, 0o644)
}

// ---- menu ----

type itemKind int

const (
	kLevel itemKind = iota
	kToggle
	kAction
)

type item struct {
	name string
	kind itemKind
	val  *int  // kLevel
	min  int   // kLevel
	max  int   // kLevel
	on   *bool // kToggle
	act  func(g *game)
}

type menu struct {
	open  bool
	sel   int
	items []item
	rep   int // key repeat counter
}

func (g *game) buildMenu() {
	s := &g.set
	g.menu.items = []item{
		{name: "RESUME", kind: kAction, act: func(g *game) { g.menu.open = false }},
		{name: "LOOK", kind: kAction, act: func(g *game) { g.cycleLook() }},
		{name: "BRIGHTNESS", kind: kLevel, val: &s.Brightness, min: 10, max: 200},
		{name: "GLOW", kind: kLevel, val: &s.Glow, min: 0, max: 200},
		{name: "PHOSPHOR TRAILS", kind: kLevel, val: &s.Trails, min: 0, max: 300},
		{name: "BEAM DWELL", kind: kLevel, val: &s.Dwell, min: 0, max: 200},
		{name: "BEAM WIDTH", kind: kLevel, val: &s.Width, min: 50, max: 200},
		{name: "INVERT Y", kind: kToggle, on: &s.InvertY},
		{name: "FULL SCREEN", kind: kToggle, on: &s.Fullscreen},
		{name: "RESET TO DEFAULTS", kind: kAction, act: func(g *game) {
			inv, fs := g.set.InvertY, g.set.Fullscreen
			g.set = defaultSettings()
			g.set.InvertY, g.set.Fullscreen = inv, fs
		}},
		{name: "QUIT", kind: kAction, act: func(g *game) { g.quit = true }},
	}
}

// cycleLook steps VECTOR MONITOR -> CLEAN LINES -> VECTOR MONITOR.
func (g *game) cycleLook() {
	b, inv, fs := g.set.Brightness, g.set.InvertY, g.set.Fullscreen
	if g.set.look() == "VECTOR MONITOR" {
		g.set = cleanSettings(defaultSettings())
	} else {
		g.set = defaultSettings()
	}
	g.set.Brightness, g.set.InvertY, g.set.Fullscreen = b, inv, fs
}

// applySettings pushes settings into the renderer and window, and saves.
func (g *game) applySettings() {
	g.set.clamp()
	g.gpu.P = g.set.Params()
	if ebiten.IsFullscreen() != g.set.Fullscreen {
		ebiten.SetFullscreen(g.set.Fullscreen)
	}
	g.set.save()
}

// menuInput handles the menu while it is open. Returns true if settings changed.
func (g *game) menuInput(ps padState) {
	m := &g.menu
	k := inpututil.IsKeyJustPressed
	up := k(ebiten.KeyUp) || k(ebiten.KeyW) || ps.upEdge
	down := k(ebiten.KeyDown) || k(ebiten.KeyS) || ps.downEdge
	// left / right repeat while held
	lh := ebiten.IsKeyPressed(ebiten.KeyLeft) || ebiten.IsKeyPressed(ebiten.KeyA) || ps.left
	rh := ebiten.IsKeyPressed(ebiten.KeyRight) || ebiten.IsKeyPressed(ebiten.KeyD) || ps.right
	left, right := false, false
	if lh || rh {
		if m.rep == 0 || (m.rep > 18 && m.rep%3 == 0) {
			left, right = lh, rh
		}
		m.rep++
	} else {
		m.rep = 0
	}
	ok := k(ebiten.KeyEnter) || k(ebiten.KeyZ) || k(ebiten.KeySpace) || ps.okEdge ||
		inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	back := k(ebiten.KeyEscape) || k(ebiten.KeyTab) || ps.backEdge || ps.menuEdge

	if back {
		m.open = false
		return
	}
	if up {
		m.sel = (m.sel + len(m.items) - 1) % len(m.items)
	}
	if down {
		m.sel = (m.sel + 1) % len(m.items)
	}
	if _, wy := ebiten.Wheel(); wy != 0 {
		if wy > 0 {
			left = false
			right = true
		} else {
			right = false
			left = true
		}
	}
	it := &m.items[m.sel]
	changed := false
	switch it.kind {
	case kLevel:
		if left {
			*it.val -= 10
			changed = true
		}
		if right {
			*it.val += 10
			changed = true
		}
		if *it.val < it.min {
			*it.val = it.min
		}
		if *it.val > it.max {
			*it.val = it.max
		}
	case kToggle:
		if left || right || ok {
			*it.on = !*it.on
			changed = true
		}
	case kAction:
		if ok || (it.name == "LOOK" && (left || right)) {
			it.act(g)
			changed = true
		}
	}
	if changed {
		g.applySettings()
	}
}

// menuStrokes draws the menu (in tube pixels gw x gh).
func (g *game) menuStrokes(out []gpu.Stroke, gw, gh float32) []gpu.Stroke {
	h := gh / 34 // glyph height
	lineH := h * 1.9
	title := "SETTINGS"
	y := gh*0.5 - lineH*float32(len(g.menu.items)+2)/2
	out = vecText(out, title, (gw-textWidth(title, h*1.4))/2, y, h*1.4, 1, 0.15, 0.1)
	y += lineH * 1.6
	colL := gw * 0.18
	colR := gw * 0.60
	for i, it := range g.menu.items {
		r, gg, b := float32(0.15), float32(0.9), float32(0.2) // game green
		if i == g.menu.sel {
			r, gg, b = 1, 0.95, 0.1 // yellow
			out = vecText(out, ">", colL-h*1.4, y, h, r, gg, b)
		}
		out = vecText(out, it.name, colL, y, h, r, gg, b)
		val := ""
		switch it.kind {
		case kLevel:
			if *it.val == 0 {
				val = "OFF"
			} else {
				val = fmt.Sprintf("%d%%", *it.val)
			}
			if i == g.menu.sel {
				val = "< " + val + " >"
			}
		case kToggle:
			val = "OFF"
			if *it.on {
				val = "ON"
			}
		case kAction:
			if it.name == "LOOK" {
				val = g.set.look()
				if i == g.menu.sel {
					val = "< " + val + " >"
				}
			}
		}
		if val != "" {
			out = vecText(out, val, colR, y, h, 0.2, 0.55, 1)
		}
		y += lineH
	}
	hint := "ARROWS / D-PAD   ENTER / A   ESC / B"
	out = vecText(out, hint, (gw-textWidth(hint, h*0.7))/2, gh-h*2.2, h*0.7, 0.5, 0.5, 0.55)
	return out
}
