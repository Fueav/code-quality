package contract

func EvaluateLegacy(status string, amount int) bool {
	return status == "settled" && amount > 0
}
