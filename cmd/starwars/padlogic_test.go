package main

import "testing"

func TestSelectIsCoinOnRelease(t *testing.T) {
	var p padTracker
	p.buttons(true, false)
	_, coin, _ := p.buttons(false, false)
	if !coin {
		t.Fatal("expected coin on Select release")
	}
}

func TestStartStarts(t *testing.T) {
	var p padTracker
	s, _, _ := p.buttons(false, true)
	if !s {
		t.Fatal("expected start")
	}
}

func TestHoldBothQuits(t *testing.T) {
	var p padTracker
	var quit bool
	for i := 0; i < quitFrames; i++ {
		_, _, quit = p.buttons(true, true)
	}
	if !quit {
		t.Fatal("expected quit after holding both")
	}
	_, coin, _ := p.buttons(false, false)
	if coin {
		t.Fatal("quit gesture must not insert a coin")
	}
}
