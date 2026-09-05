package report

// The two pieces of arithmetic a Phase 11 report can get *wrong* rather than
// merely miss: the movement against the previous window, and the direction a
// series is heading. Both live here, apart from the database and the HTTP
// layer, so they can be table-tested and so every endpoint quotes the same
// number for the same facts.

// ChangePct is the movement from the previous window to this one, as a
// percentage rounded to one decimal place.
//
// It is nil when the previous window is empty: a rise from nothing is not
// "+100%", it is a first month, and quoting a number there would be inventing
// one. A fall to nothing from a non-empty previous window is -100%, which is
// exactly what happened.
func ChangePct(current, previous int64) *float64 {
	if previous == 0 {
		return nil
	}
	pct := float64(current-previous) / float64(previous) * 100
	rounded := round1(pct)
	return &rounded
}

// Slope is the least-squares gradient of a series against its bucket index:
// "collections are moving by this much per bucket". It is the trend line a
// sparkline draws, reduced to the one number a card can show.
//
// Fewer than two points have no gradient — a single month is a level, not a
// trend — and the answer there is 0 rather than a refusal, because a series
// with one bucket in it is a legitimate window, not an error.
//
// The result is rounded to one decimal place: the inputs are whole shillings,
// and quoting a gradient to fifteen digits would imply a precision the data
// does not have.
func Slope(values []int64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	// x is the bucket index 0…n-1, so both means are exact in float64 for any
	// series short enough to be a series (MaxBuckets is 400).
	meanX := float64(n-1) / 2
	var sumY float64
	for _, v := range values {
		sumY += float64(v)
	}
	meanY := sumY / float64(n)

	var num, den float64
	for i, v := range values {
		dx := float64(i) - meanX
		num += dx * (float64(v) - meanY)
		den += dx * dx
	}
	if den == 0 {
		return 0
	}
	return round1(num / den)
}

// Rate is `part / whole` as a fraction, nil when there is no whole to divide
// by: a collection rate against nothing expected is undefined, not 0% and not
// 100%.
func Rate(part, whole int64) *float64 {
	if whole == 0 {
		return nil
	}
	v := float64(part) / float64(whole)
	return &v
}

// round1 rounds half away from zero to one decimal place, without pulling in
// math for the one operation.
func round1(v float64) float64 {
	if v < 0 {
		return -float64(int64(-v*10+0.5)) / 10
	}
	return float64(int64(v*10+0.5)) / 10
}
