package main

import "github.com/hajimehoshi/ebiten/v2"

// Gamepad (standard layout: Xbox / PlayStation / Switch Pro / Steam Deck).
//
//	Left stick            yoke
//	A / right trigger     fire (also starts a game after a coin)
//	B / left trigger      fire 2
//	X, Y                  thumb buttons
//	Start                 fire / start
//	Select (Back)         insert coin (on release)
//	Select + Start held   quit (about one second)
type padState struct {
	sx, sy                     float64
	stickActive                bool
	fire1, fire2, fire3, fire4 bool
	start                      bool
	coin                       bool // one-frame pulse
	quit                       bool
}

func (t *padTracker) poll() padState {
	var s padState
	var sel, start bool
	ids := ebiten.AppendGamepadIDs(nil)
	t.connected = false
	for _, id := range ids {
		if !ebiten.IsStandardGamepadLayoutAvailable(id) {
			continue
		}
		t.connected = true
		b := func(btn ebiten.StandardGamepadButton) bool { return ebiten.IsStandardGamepadButtonPressed(id, btn) }
		ax := func(a ebiten.StandardGamepadAxis) float64 { return ebiten.StandardGamepadAxisValue(id, a) }
		hx := ax(ebiten.StandardGamepadAxisLeftStickHorizontal)
		hy := ax(ebiten.StandardGamepadAxisLeftStickVertical)
		if hx*hx+hy*hy > 0.02 {
			s.stickActive = true
			s.sx, s.sy = hx, hy
		}
		dx, dy := 0.0, 0.0
		if b(ebiten.StandardGamepadButtonLeftLeft) {
			dx = -1
		}
		if b(ebiten.StandardGamepadButtonLeftRight) {
			dx = 1
		}
		if b(ebiten.StandardGamepadButtonLeftTop) {
			dy = -1
		}
		if b(ebiten.StandardGamepadButtonLeftBottom) {
			dy = 1
		}
		if dx != 0 || dy != 0 {
			s.stickActive = true
			s.sx, s.sy = dx, dy
		}
		s.fire1 = s.fire1 || b(ebiten.StandardGamepadButtonRightBottom) || b(ebiten.StandardGamepadButtonFrontBottomRight)
		s.fire2 = s.fire2 || b(ebiten.StandardGamepadButtonRightRight) || b(ebiten.StandardGamepadButtonFrontBottomLeft)
		s.fire3 = s.fire3 || b(ebiten.StandardGamepadButtonRightLeft)
		s.fire4 = s.fire4 || b(ebiten.StandardGamepadButtonRightTop)
		sel = sel || b(ebiten.StandardGamepadButtonCenterLeft)
		start = start || b(ebiten.StandardGamepadButtonCenterRight)
	}
	s.start, s.coin, s.quit = t.buttons(sel, start)
	return s
}
