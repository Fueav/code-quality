package pricing

import "math"

func Round(value float64) float64 {
	return math.Floor(value + 0.5)
}
