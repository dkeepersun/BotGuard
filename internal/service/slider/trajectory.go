package slider

import "math"

// Point is one trajectory sample: milliseconds since pointer down, and the
// piece position in puzzle pixels (x) plus vertical pointer offset (y).
type Point struct {
	T, X, Y float64
}

const MaxPoints = 400

// Rejection reasons, logged server-side only. The browser always sees the
// single ANSWER_INVALID code so it cannot learn which check failed.
const (
	ReasonPosition         = "POSITION"
	ReasonTooFewPoints     = "TOO_FEW_POINTS"
	ReasonDuration         = "DURATION"
	ReasonTimeOrder        = "TIME_ORDER"
	ReasonStart            = "START"
	ReasonEndMismatch      = "END_MISMATCH"
	ReasonTeleport         = "TELEPORT"
	ReasonEqualIntervals   = "EQUAL_INTERVALS"
	ReasonConstantVelocity = "CONSTANT_VELOCITY"
	ReasonStraightLine     = "STRAIGHT_LINE"
)

// Check returns "" when the answer is accepted, otherwise the first failing
// reason. input is the pointer type: mouse, touch, pen or keyboard.
func Check(answerX, submittedX float64, tolerance int, points []Point, input string) string {
	if math.Abs(submittedX-answerX) > float64(tolerance) {
		return ReasonPosition
	}
	minPoints, minMs := 8, 300.0
	if input == "keyboard" {
		minPoints = 2
	}
	if len(points) < minPoints || len(points) > MaxPoints {
		return ReasonTooFewPoints
	}
	first, last := points[0], points[len(points)-1]
	if d := last.T - first.T; d < minMs || d > 30_000 {
		return ReasonDuration
	}
	for i := 1; i < len(points); i++ {
		if points[i].T < points[i-1].T {
			return ReasonTimeOrder
		}
	}
	if first.X < -2 || first.X > 6 {
		return ReasonStart
	}
	if math.Abs(last.X-submittedX) > 3 {
		return ReasonEndMismatch
	}
	if input == "keyboard" {
		// Arrow keys move in fixed steps at the key-repeat rate: none of the
		// pointer heuristics below apply.
		return ""
	}

	total := math.Abs(last.X - first.X)
	var dts, speeds []float64
	maxY := 0.0
	for i := 1; i < len(points); i++ {
		dx := math.Abs(points[i].X - points[i-1].X)
		dt := points[i].T - points[i-1].T
		if total > 20 && dx > total*0.5 {
			return ReasonTeleport
		}
		dts = append(dts, dt)
		if dt > 0 && dx > 0 {
			speeds = append(speeds, dx/dt)
		}
		maxY = math.Max(maxY, math.Abs(points[i].Y-first.Y))
	}
	if len(dts) >= 8 && spread(dts) < 0.05 {
		return ReasonEqualIntervals
	}
	// Humans accelerate, decelerate and correct; a scripted linear move does not.
	if len(speeds) >= 8 && cv(speeds) < 0.08 {
		return ReasonConstantVelocity
	}
	if input == "mouse" && total > 60 && maxY < 0.5 {
		return ReasonStraightLine
	}
	return ""
}

// spread is the max deviation of values from their mean, in the same unit.
func spread(v []float64) float64 {
	mean := 0.0
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	worst := 0.0
	for _, x := range v {
		worst = math.Max(worst, math.Abs(x-mean))
	}
	return worst
}

// cv is the coefficient of variation (stddev / mean).
func cv(v []float64) float64 {
	mean := 0.0
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	if mean == 0 {
		return 0
	}
	ss := 0.0
	for _, x := range v {
		ss += (x - mean) * (x - mean)
	}
	return math.Sqrt(ss/float64(len(v))) / mean
}
