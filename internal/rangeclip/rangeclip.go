// Package rangeclip holds the byte-window arithmetic shared by the ziphttp
// and tarhttp writers: both regenerate a deterministic archive stream and
// emit only the part that falls inside the requested [begin, end] window.
package rangeclip

// Clip clips a chunk of length l located at logical offset current against
// the requested inclusive window [begin, end]; end == -1 means unbounded.
// skip means the chunk lies entirely outside the window; otherwise the
// caller emits chunk[b:e]. partial reports that the chunk was cut on either
// side (used to issue upstream HTTP Range requests for file content).
func Clip(current, begin, end, l int64) (partial bool, skip bool, b int64, e int64) {
	e = l
	if current+l <= begin || (current > end && end != -1) {
		partial = true
		skip = true
		return
	}
	if current+l > begin && current < begin {
		partial = true
		b = begin - current
	}
	if current+l > end && current <= end {
		partial = true
		e = end - current + 1
	}
	return
}
