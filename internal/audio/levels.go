package audio

import "math"

// dbfsFloor is the lower bound of the meter scale in dB. Anything quieter
// is reported as the floor: below it the value carries no information for
// a UI meter and log10 of silence is not a number.
const dbfsFloor = -96.0

// fullScaleSineRMS is the 0 dBFS reference in the int16 scale: the RMS of
// a sine that spans full scale, i.e. 32768 / sqrt(2).
var fullScaleSineRMS = fullScale / math.Sqrt2

// RMS returns the root mean square of the frame in the int16 scale.
func RMS(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, s := range pcm {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

// DBFS converts an RMS value in int16 scale to dBFS (0 dBFS = full scale
// sine), clamped to a floor of -96 dB.
func DBFS(rms float64) float64 {
	// Also catches NaN: any comparison with NaN is false.
	if !(rms > 0) {
		return dbfsFloor
	}
	db := 20 * math.Log10(rms/fullScaleSineRMS)
	if db < dbfsFloor {
		return dbfsFloor
	}
	return db
}
