package main

const quitFrames = 60

// padTracker turns the Select / Start buttons into game actions.
type padTracker struct {
	selHeld   int  // frames Select has been down
	selUsed   bool // Start joined this Select hold (the quit gesture)
	bothHeld  int  // frames Select and Start were down together
	prevSel   bool
	prevView  bool
	prevLB    bool
	connected bool
}

// buttons is called once per frame. Select is a coin when it is let go; Start
// starts a game; holding both for quitFrames frames quits and does nothing else.
func (t *padTracker) buttons(sel, start bool) (start1, coin, quit bool) {
	if sel && start {
		t.bothHeld++
		t.selUsed = true
		quit = t.bothHeld >= quitFrames
	} else {
		t.bothHeld = 0
	}
	if start && !sel && !t.selUsed {
		start1 = true
	}
	if sel {
		t.selHeld++
	}
	if !sel && t.prevSel {
		coin = !t.selUsed && t.selHeld < 40
		t.selHeld, t.selUsed = 0, false
	}
	t.prevSel = sel
	return
}
