package pricing

import "math"

func Round(value float64) float64 {
	return math.RoundToEven(value)
}
