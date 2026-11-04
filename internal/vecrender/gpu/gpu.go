// Package gpu is the real-time Ebitengine version of vecrender.Soft.
package gpu

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"

	"starwars/internal/vecrender"
)

// GPU is the real-time renderer. Same model as Soft: Gaussian beam strokes
// at full output resolution added together, endpoint dwell, speed dimming,
// phosphor persistence, bloom and a filmic tone map. Strokes are drawn into
// an offscreen "phosphor" image at reduced energy (8-bit headroom) and the
// final pass scales them back up through the tone curve.
type GPU struct {
	P vecrender.Params

	w, h      int
	phos      *ebiten.Image // accumulated energy (persistence lives here)
	scratch   *ebiten.Image
	half      *ebiten.Image    // 1/2 size step of the bloom chain
	small     [2]*ebiten.Image // bloom chain at 1/4 size
	beam      *ebiten.Shader
	blurSh    *ebiten.Shader
	compose   *ebiten.Shader
	decaySh   *ebiten.Shader
	vtx       []ebiten.Vertex
	idx       []uint16
	lastFrame int
}

// energyScale keeps summed strokes inside 8 bits; compose divides it back out.
const energyScale = 0.25

const beamSrc = `//kage:unit pixels
package main

var InvTwoSigma2 float

// custom.xy is the offset from the beam centre line (x along, y across) in
// pixels. Body quads have x = 0 everywhere; dot quads use both.
func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
	d2 := dot(custom.xy, custom.xy)
	v := exp(-d2 * InvTwoSigma2)
	return vec4(color.rgb*v, v)
}
`

const decaySrc = `//kage:unit pixels
package main

var Keep float

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	return imageSrc0At(src) * Keep
}
`

const blurSrc = `//kage:unit pixels
package main

var Dir vec2
var Sigma float

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	acc := vec4(0)
	wsum := 0.0
	for i := -12; i <= 12; i++ {
		f := float(i)
		w := exp(-f * f / (2 * Sigma * Sigma))
		acc += imageSrc0At(src+Dir*f) * w
		wsum += w
	}
	return acc / wsum
}
`

const composeSrc = `//kage:unit pixels
package main

var Exposure float
var Bloom float
var Energy float

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	c := imageSrc0At(src).rgb / Energy
	b := imageSrc1At(src).rgb / Energy
	c += b * Bloom
	hot := max(c.r, max(c.g, c.b))
	if hot > 1.5 {
		c += vec3((hot - 1.5) * 0.18)
	}
	c = vec3(1) - exp(-c*Exposure)
	c = pow(c, vec3(1/2.2))
	return vec4(c, 1)
}
`

// NewGPU compiles the shaders.
func New(p vecrender.Params) (*GPU, error) {
	g := &GPU{P: p}
	var err error
	if g.beam, err = ebiten.NewShader([]byte(beamSrc)); err != nil {
		return nil, err
	}
	if g.decaySh, err = ebiten.NewShader([]byte(decaySrc)); err != nil {
		return nil, err
	}
	if g.blurSh, err = ebiten.NewShader([]byte(blurSrc)); err != nil {
		return nil, err
	}
	if g.compose, err = ebiten.NewShader([]byte(composeSrc)); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *GPU) ensure(w, h int) {
	if g.phos != nil && g.w == w && g.h == h {
		return
	}
	for _, im := range []*ebiten.Image{g.phos, g.scratch, g.half, g.small[0], g.small[1]} {
		if im != nil {
			im.Deallocate()
		}
	}
	g.w, g.h = w, h
	g.phos = ebiten.NewImage(w, h)
	g.scratch = ebiten.NewImage(w, h)
	g.half = ebiten.NewImage((w+1)/2, (h+1)/2)
	sw, sh := (w+3)/4, (h+3)/4
	g.small[0] = ebiten.NewImage(sw, sh)
	g.small[1] = ebiten.NewImage(sw, sh)
}

// Stroke is one beam stroke already in output pixels.
type Stroke struct {
	X0, Y0, X1, Y1 float32
	R, G, B        float32 // colour x intensity, 0..1
}

func (g *GPU) quad(x0, y0, x1, y1, x2, y2, x3, y3 float32, s0, t0, s1, t1, s2, t2, s3, t3 float32, r, gg, b float32) {
	base := uint16(len(g.vtx))
	g.vtx = append(g.vtx,
		ebiten.Vertex{DstX: x0, DstY: y0, Custom0: s0, Custom1: t0, ColorR: r, ColorG: gg, ColorB: b, ColorA: 1},
		ebiten.Vertex{DstX: x1, DstY: y1, Custom0: s1, Custom1: t1, ColorR: r, ColorG: gg, ColorB: b, ColorA: 1},
		ebiten.Vertex{DstX: x2, DstY: y2, Custom0: s2, Custom1: t2, ColorR: r, ColorG: gg, ColorB: b, ColorA: 1},
		ebiten.Vertex{DstX: x3, DstY: y3, Custom0: s3, Custom1: t3, ColorR: r, ColorG: gg, ColorB: b, ColorA: 1},
	)
	g.idx = append(g.idx, base, base+1, base+2, base+1, base+3, base+2)
}

func (g *GPU) flush(dst *ebiten.Image, sigma float64) {
	if len(g.idx) == 0 {
		return
	}
	op := &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendLighter}
	op.Uniforms = map[string]any{"InvTwoSigma2": float32(1 / (2 * sigma * sigma))}
	dst.DrawTrianglesShader(g.vtx, g.idx, g.beam, op)
	g.vtx, g.idx = g.vtx[:0], g.idx[:0]
}

// Draw renders strokes (in output pixels, w x h output) into screen at (ox, oy).
// newFrame says whether this is a fresh display list (decay applies per tube
// refresh, not per host frame).
func (g *GPU) Draw(screen *ebiten.Image, ox, oy float64, w, h int, strokes []Stroke, newFrame bool) {
	g.ensure(w, h)
	scale := float64(h) / 1080
	sigma := g.P.BeamWidth * scale
	if sigma < 0.5 {
		sigma = 0.5
	}
	if newFrame {
		// Phosphor decay: phos = phos * Persistence.
		g.scratch.Clear()
		op := &ebiten.DrawRectShaderOptions{}
		op.Images[0] = g.phos
		op.Uniforms = map[string]any{"Keep": float32(g.P.Persistence)}
		g.scratch.DrawRectShader(w, h, g.decaySh, op)
		g.phos, g.scratch = g.scratch, g.phos

		reach := float32(3.2 * sigma)
		for _, s := range strokes {
			dx, dy := s.X1-s.X0, s.Y1-s.Y0
			l := float32(math.Hypot(float64(dx), float64(dy)))
			dim := float32(1)
			if g.P.SpeedDim > 0 && float64(l) > 4*scale {
				dim = float32(math.Pow(4*scale/float64(l), g.P.SpeedDim*0.5))
			}
			e := float32(g.P.Gain*energyScale) * dim
			if l >= 0.5 {
				ux, uy := dx/l, dy/l
				nx, ny := -uy*reach, ux*reach
				g.quad(s.X0+nx, s.Y0+ny, s.X1+nx, s.Y1+ny, s.X0-nx, s.Y0-ny, s.X1-nx, s.Y1-ny,
					0, reach, 0, reach, 0, -reach, 0, -reach,
					s.R*e, s.G*e, s.B*e)
			}
			// Endpoint dwell (and round caps). Dots get the full dwell.
			dg := float32(g.P.DotGain*0.5*energyScale) + e
			if l < 0.5 {
				dg = float32(g.P.DotGain*1.6*energyScale) + e
			}
			ends := [][2]float32{{s.X0, s.Y0}, {s.X1, s.Y1}}
			if l < 0.5 {
				ends = ends[:1] // a dot has one position
			}
			for _, p := range ends {
				r := reach * 1.2
				g.quad(p[0]-r, p[1]-r, p[0]+r, p[1]-r, p[0]-r, p[1]+r, p[0]+r, p[1]+r,
					-r, -r, r, -r, -r, r, r, r,
					s.R*dg*0.55, s.G*dg*0.55, s.B*dg*0.55)
			}
			if len(g.vtx) > 60000 {
				g.flush(g.phos, sigma)
			}
		}
		g.flush(g.phos, sigma)

		// Bloom: quarter-size copy, separable blur.
		if g.P.Bloom > 0 {
			sw, sh := g.small[0].Bounds().Dx(), g.small[0].Bounds().Dy()
			// Two halvings so every source pixel contributes (no aliasing of thin lines).
			hw, hh := g.half.Bounds().Dx(), g.half.Bounds().Dy()
			g.half.Clear()
			dop := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
			dop.GeoM.Scale(float64(hw)/float64(w), float64(hh)/float64(h))
			g.half.DrawImage(g.phos, dop)
			g.small[0].Clear()
			dop = &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
			dop.GeoM.Scale(float64(sw)/float64(hw), float64(sh)/float64(hh))
			g.small[0].DrawImage(g.half, dop)
			bs := float32(g.P.BloomRadius * scale / 4)
			for pass, dir := range [][2]float32{{1, 0}, {0, 1}} {
				src, dst := g.small[pass], g.small[1-pass]
				dst.Clear()
				op := &ebiten.DrawRectShaderOptions{}
				op.Images[0] = src
				op.Uniforms = map[string]any{"Dir": dir[:], "Sigma": bs}
				dst.DrawRectShader(sw, sh, g.blurSh, op)
			}
		}
		// Upscale the blurred glow to full size for the compose pass.
		g.scratch.Clear()
		if g.P.Bloom > 0 {
			sw, sh := g.small[0].Bounds().Dx(), g.small[0].Bounds().Dy()
			uop := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
			uop.GeoM.Scale(float64(w)/float64(sw), float64(h)/float64(sh))
			g.scratch.DrawImage(g.small[0], uop)
		}
	}
	// Compose to the screen.
	op := &ebiten.DrawRectShaderOptions{}
	op.GeoM.Translate(ox, oy)
	op.Images[0] = g.phos
	op.Images[1] = g.scratch
	op.Uniforms = map[string]any{
		"Exposure": float32(g.P.Exposure), "Bloom": float32(g.P.Bloom), "Energy": float32(energyScale),
	}
	screen.DrawRectShader(w, h, g.compose, op)
}
