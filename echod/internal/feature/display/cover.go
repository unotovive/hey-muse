package display

// coverStops is what a hand held over the camera does: it stops a ring if one is going, and otherwise
// cancels Muse's turn if one is under way (which also stops the spoken answer). It reports whether it
// stopped anything.
func coverStops(endRing func() bool, turnBusy func() bool, cancelTurn func()) bool {
	if endRing() {
		return true
	}
	if turnBusy() {
		cancelTurn()
		return true
	}
	return false
}
