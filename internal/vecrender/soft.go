// Package vecrender draws AVG display lists the way a colour XY monitor
// shows them. soft.go is the CPU reference implementation (used for headless
// snapshots and as the spec the GPU shaders must match): every stroke is a
// Gaussian beam evaluated per output pixel at full output resolution, added
// into a linear HDR buffer, then phosphor persistence, bloom and tone mapping.
package vecrender

import (
	"image"
	"math"

	"starwars/internal/avg"
)

// Params are the look controls. Defaults() is the shipped look.
type Params struct {
	BeamWidth   float64 // beam sigma, in output pixels at 1080 lines (scaled with height)
	Gain        float64 // overall stroke energy
	DotGain     float64 // extra energy where the beam dwells (endpoints, dots)
	SpeedDim    float64 // 0..1: how much long fast strokes dim per unit length
	Persistence float64 // fraction of last frame kept (phosphor decay), 0 = off
	Bloom       float64 // glow strength
	BloomRadius float64 // glow sigma in output pixels at 1080 lines
	Exposure    float64
}

// Defaults is the standard look.
func Defaults() Params {
	return Params{BeamWidth: 1.1, Gain: 1.7, DotGain: 0.8, SpeedDim: 0.35,
		Persistence: 0.18, Bloom: 0.75, BloomRadius: 7, Exposure: 1.4}
}

// Clean is sharp lines, no glow or trails.
func Clean() Params {
	return Params{BeamWidth: 0.7, Gain: 1.6, DotGain: 1.2, SpeedDim: 0,
		Persistence: 0, Bloom: 0, BloomRadius: 1, Exposure: 1.2}
}

// Soft is a software vector monitor of a fixed size.
type Soft struct {
	W, H  int
	P     Params
	accum []float32 // RGB HDR, persistence carried here
	tmp   []float32
}

// NewSoft makes a renderer for a W x H output.
func NewSoft(w, h int, p Params) *Soft {
	return &Soft{W: w, H: h, P: p, accum: make([]float32, w*h*3), tmp: make([]float32, w*h*3)}
}

// Draw adds one display list (one tube refresh) and returns the tone-mapped image.
func (s *Soft) Draw(segs []avg.Segment) *image.RGBA {
	w, h := s.W, s.H
	scale := float64(h) / 1080
	sigma := s.P.BeamWidth * scale
	if sigma < 0.45 {
		sigma = 0.45
	}
	// Phosphor decay.
	k := float32(s.P.Persistence)
	for i := range s.accum {
		s.accum[i] *= k
	}
	// Beam space -> screen: the 250 x 280 AVG area fills the 4:3 tube.
	sx := float64(w) / avg.Width
	sy := float64(h) / avg.Height
	inv2s2 := 1 / (2 * sigma * sigma)
	reach := 3.2 * sigma
	norm := 1 / (sigma * math.Sqrt(2*math.Pi)) // unit energy per unit length across the beam
	for _, sg := range segs {
		x0, y0 := float64(sg.X0)*sx, float64(sg.Y0)*sy
		x1, y1 := float64(sg.X1)*sx, float64(sg.Y1)*sy
		dx, dy := x1-x0, y1-y0
		l2 := dx*dx + dy*dy
		l := math.Sqrt(l2)
		// Long strokes are drawn at speed: less energy per unit length.
		dim := 1.0
		if s.P.SpeedDim > 0 && l > 4*scale {
			dim = math.Pow(4*scale/l, s.P.SpeedDim*0.5)
		}
		e := s.P.Gain * dim * norm * sigma // core height ~ Gain
		dot := s.P.DotGain * 0.5
		if l < 0.5 {
			dot = s.P.DotGain * 1.6 // a stationary beam is the brightest thing on the tube
		}
		r, g, b := float64(sg.R), float64(sg.G), float64(sg.B)
		minx := int(math.Floor(math.Min(x0, x1) - reach))
		maxx := int(math.Ceil(math.Max(x0, x1) + reach))
		miny := int(math.Floor(math.Min(y0, y1) - reach))
		maxy := int(math.Ceil(math.Max(y0, y1) + reach))
		if minx < 0 {
			minx = 0
		}
		if miny < 0 {
			miny = 0
		}
		if maxx > w-1 {
			maxx = w - 1
		}
		if maxy > h-1 {
			maxy = h - 1
		}
		for py := miny; py <= maxy; py++ {
			fy := float64(py) + 0.5
			for px := minx; px <= maxx; px++ {
				fx := float64(px) + 0.5
				// distance to the segment
				t := 0.0
				if l2 > 0 {
					t = ((fx-x0)*dx + (fy-y0)*dy) / l2
					if t < 0 {
						t = 0
					} else if t > 1 {
						t = 1
					}
				}
				qx, qy := x0+t*dx-fx, y0+t*dy-fy
				d2 := qx*qx + qy*qy
				if d2 > reach*reach {
					continue
				}
				v := e * math.Exp(-d2*inv2s2)
				// dwell at the two ends
				ax, ay := fx-x0, fy-y0
				bx, by := fx-x1, fy-y1
				v += dot * (math.Exp(-(ax*ax+ay*ay)*inv2s2*0.7) + math.Exp(-(bx*bx+by*by)*inv2s2*0.7))
				o := (py*w + px) * 3
				s.accum[o] += float32(v * r)
				s.accum[o+1] += float32(v * g)
				s.accum[o+2] += float32(v * b)
			}
		}
	}
	// Bloom: separable Gaussian blur of the HDR buffer, added at output.
	out := s.accum
	if s.P.Bloom > 0 {
		blur(s.accum, s.tmp, w, h, s.P.BloomRadius*scale)
		out = s.tmp
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	ex := s.P.Exposure
	bl := float32(s.P.Bloom)
	for i := 0; i < w*h; i++ {
		var c [3]float64
		for ch := 0; ch < 3; ch++ {
			v := float64(s.accum[i*3+ch])
			if s.P.Bloom > 0 {
				v += float64(out[i*3+ch] * bl)
			}
			c[ch] = v
		}
		// Very hot beam spots desaturate towards white, as phosphor saturates.
		hot := math.Max(c[0], math.Max(c[1], c[2]))
		if hot > 1.5 {
			sp := (hot - 1.5) * 0.18
			for ch := range c {
				c[ch] += sp
			}
		}
		var px [3]uint8
		for ch := 0; ch < 3; ch++ {
			v := 1 - math.Exp(-c[ch]*ex) // filmic-ish shoulder, black stays black
			v = math.Pow(v, 1/2.2)
			px[ch] = uint8(math.Min(255, v*255+0.5))
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = px[0], px[1], px[2], 255
	}
	return img
}

func blur(src, dst []float32, w, h int, sigma float64) {
	if sigma < 0.5 {
		copy(dst, src)
		return
	}
	r := int(math.Ceil(sigma * 3))
	ker := make([]float32, 2*r+1)
	var sum float32
	for i := -r; i <= r; i++ {
		k := float32(math.Exp(-float64(i*i) / (2 * sigma * sigma)))
		ker[i+r] = k
		sum += k
	}
	for i := range ker {
		ker[i] /= sum
	}
	mid := make([]float32, len(src))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var a [3]float32
			for i := -r; i <= r; i++ {
				xx := x + i
				if xx < 0 || xx >= w {
					continue
				}
				o := (y*w + xx) * 3
				k := ker[i+r]
				a[0] += src[o] * k
				a[1] += src[o+1] * k
				a[2] += src[o+2] * k
			}
			o := (y*w + x) * 3
			mid[o], mid[o+1], mid[o+2] = a[0], a[1], a[2]
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var a [3]float32
			for i := -r; i <= r; i++ {
				yy := y + i
				if yy < 0 || yy >= h {
					continue
				}
				o := (yy*w + x) * 3
				k := ker[i+r]
				a[0] += mid[o] * k
				a[1] += mid[o+1] * k
				a[2] += mid[o+2] * k
			}
			o := (y*w + x) * 3
			dst[o], dst[o+1], dst[o+2] = a[0], a[1], a[2]
		}
	}
}
