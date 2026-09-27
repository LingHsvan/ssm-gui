// Copyright (C) 2024, 2025, 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package stage

// BanG Dream! Our Notes (sirius) judge-line geometry.
//
// Unlike bang.go / pjsk.go, which were fitted from screenshots of their own
// stages, these two constants were measured on the Our Notes playfield itself.
// Our Notes draws the lane as a triangle converging just above the top edge of
// the screen, so the lane gets wider the lower you go.
//
// Measurement basis: two landscape gameplay screenshots, 1920x883 each. That
// is the device's full logical screen (2608x1200 landscape, aspect 2.173) at
// 0.736 scale, so height-relative ratios carry over unchanged.
//
//	y=665   upper magenta band edge spans x 360..1558  -> W = 1198 px
//	y=734   lower magenta band edge spans x 301..1616  -> W = 1315 px
//	y=800   lane edges                                  -> W = 1452 px
//	y=860   lane edges                                  -> W = 1528 px
//
// All four are centred on x = 959.5 (== width/2, i.e. `middle` below), and fit
// halfWidth(y) = 0.8478*y + 35.2 (the magenta pair) or 0.8672*y + 23.4 (least
// squares over all four, residuals <= 9 px). That extrapolates a vanishing
// point at y ~ -40, just above the top edge — exactly what the screenshots
// show. Two visible size-6 notes (189 px wide at y=428, 262 px at y=600)
// independently confirm the magenta span is the full 24-half-lane field:
// size/24 * W(y) predicts 199 px and 262 px.
//
// The two magenta lines are the top and bottom edge of the judgement band (the
// lower one looks wider because it is nearer the camera; the 69 px gap is in
// the right range for the game's tapArea). The judge line sits in the middle
// of that band, which also lands at the ~0.78-0.79 of screen height that BanG
// (0.783) and PJSK (0.792) use, and keeps the touch clear of the Android
// bottom navigation gesture zone.
//
// On a 2608x1200 device this puts the judge line at y ~= 951 and the field at
// x 450..2158 (1708 px wide, ~142 px per physical lane).
//
// If edge lanes turn out to miss on device, the per-line alternatives are:
//
//	upper magenta  (y=665):  onLaneHalfRatio = 0.6784  onLineRatio = 0.7531
//	lower magenta  (y=734):  onLaneHalfRatio = 0.7446  onLineRatio = 0.8313
//
// and if nothing registers at all, the analytically derived judge plane (which
// sits lower on screen and therefore comes with a wider field) is:
//
//	analytic judge (y=831):  onLaneHalfRatio = 0.8378  onLineRatio = 0.9411
const (
	onLaneHalfRatio = 0.7115
	onLineRatio     = 0.7922
)

// OurNotesJudgeLinePos returns the horizontal pixel range of the lane field
// and the y pixel of the judgement line for the given screen size (the caller
// passes the logical landscape width/height, same convention as the other
// calculators in this package).
func OurNotesJudgeLinePos(width, height float64) (float64, float64, float64) {
	half := height * onLaneHalfRatio
	middle := width / 2
	return middle - half, middle + half, height * onLineRatio
}
