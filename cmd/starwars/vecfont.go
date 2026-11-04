package main

import (
	"strconv"
	"strings"

	"starwars/internal/vecrender/gpu"
)

// A small stroke font on a 4 x 6 grid (y down), so the menu is drawn by the
// beam like everything else on the tube. Each glyph is polylines separated
// by ";", points "x,y" separated by spaces. A single point is a dot.
var glyphSrc = map[rune]string{
	'A':  "0,6 0,2 2,0 4,2 4,6; 0,3 4,3",
	'B':  "0,3 3,3 4,4 4,5 3,6 0,6 0,0 3,0 4,1 4,2 3,3",
	'C':  "4,0 0,0 0,6 4,6",
	'D':  "0,0 0,6 2,6 4,4 4,2 2,0 0,0",
	'E':  "4,0 0,0 0,6 4,6; 0,3 3,3",
	'F':  "4,0 0,0 0,6; 0,3 3,3",
	'G':  "4,1 4,0 0,0 0,6 4,6 4,3 2,3",
	'H':  "0,0 0,6; 4,0 4,6; 0,3 4,3",
	'I':  "1,0 3,0; 2,0 2,6; 1,6 3,6",
	'J':  "4,0 4,6 0,6 0,4",
	'K':  "0,0 0,6; 4,0 0,3 4,6",
	'L':  "0,0 0,6 4,6",
	'M':  "0,6 0,0 2,3 4,0 4,6",
	'N':  "0,6 0,0 4,6 4,0",
	'O':  "0,0 4,0 4,6 0,6 0,0",
	'P':  "0,6 0,0 4,0 4,3 0,3",
	'Q':  "0,0 4,0 4,6 0,6 0,0; 2,4 4,6",
	'R':  "0,6 0,0 4,0 4,3 0,3 4,6",
	'S':  "4,0 0,0 0,3 4,3 4,6 0,6",
	'T':  "0,0 4,0; 2,0 2,6",
	'U':  "0,0 0,6 4,6 4,0",
	'V':  "0,0 2,6 4,0",
	'W':  "0,0 1,6 2,3 3,6 4,0",
	'X':  "0,0 4,6; 4,0 0,6",
	'Y':  "0,0 2,3 4,0; 2,3 2,6",
	'Z':  "0,0 4,0 0,6 4,6",
	'0':  "0,0 4,0 4,6 0,6 0,0; 4,0 0,6",
	'1':  "1,1 2,0 2,6; 1,6 3,6",
	'2':  "0,0 4,0 4,3 0,3 0,6 4,6",
	'3':  "0,0 4,0 4,6 0,6; 1,3 4,3",
	'4':  "0,0 0,3 4,3; 4,0 4,6",
	'5':  "4,0 0,0 0,3 4,3 4,6 0,6",
	'6':  "4,0 0,0 0,6 4,6 4,3 0,3",
	'7':  "0,0 4,0 4,6",
	'8':  "0,0 4,0 4,6 0,6 0,0; 0,3 4,3",
	'9':  "4,3 0,3 0,0 4,0 4,6 0,6",
	'%':  "0,6 4,0; 0,0 1,0 1,1 0,1 0,0; 3,5 4,5 4,6 3,6 3,5",
	'-':  "1,3 3,3",
	'+':  "1,3 3,3; 2,2 2,4",
	'<':  "3,1 1,3 3,5",
	'>':  "1,1 3,3 1,5",
	'.':  "2,6",
	':':  "2,2; 2,5",
	'/':  "0,6 4,0",
	'(':  "3,0 2,1 2,5 3,6",
	')':  "1,0 2,1 2,5 1,6",
	'=':  "1,2 3,2; 1,4 3,4",
	'\'': "2,0 2,2",
}

var glyphs = map[rune][][][2]float32{}

func init() {
	for r, src := range glyphSrc {
		var lines [][][2]float32
		for _, part := range strings.Split(src, ";") {
			var pl [][2]float32
			for _, pt := range strings.Fields(part) {
				xy := strings.Split(pt, ",")
				x, _ := strconv.ParseFloat(xy[0], 32)
				y, _ := strconv.ParseFloat(xy[1], 32)
				pl = append(pl, [2]float32{float32(x), float32(y)})
			}
			lines = append(lines, pl)
		}
		glyphs[r] = lines
	}
}

// textWidth is the width of s at glyph height h.
func textWidth(s string, h float32) float32 {
	return float32(len([]rune(s))) * h
}

// vecText appends strokes for s with its top-left at (x, y), glyph height h.
func vecText(out []gpu.Stroke, s string, x, y, h, r, g, b float32) []gpu.Stroke {
	u := h / 6
	for _, c := range strings.ToUpper(s) {
		for _, pl := range glyphs[c] {
			if len(pl) == 1 {
				px, py := x+pl[0][0]*u, y+pl[0][1]*u
				out = append(out, gpu.Stroke{X0: px, Y0: py, X1: px, Y1: py, R: r, G: g, B: b})
				continue
			}
			for i := 1; i < len(pl); i++ {
				out = append(out, gpu.Stroke{
					X0: x + pl[i-1][0]*u, Y0: y + pl[i-1][1]*u,
					X1: x + pl[i][0]*u, Y1: y + pl[i][1]*u,
					R: r, G: g, B: b,
				})
			}
		}
		x += h // 4 units of glyph + 2 of space
	}
	return out
}
