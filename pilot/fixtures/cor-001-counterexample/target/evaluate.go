package contract

func EvaluateContractV2(status string, amount int) bool {
	switch status {
	case "settled":
		return amount > 0
	case "cancelled", "pending":
		return false
	default:
		return false
	}
}

func HandleV2(status string, amount int) bool {
	return EvaluateContractV2(status, amount)
}
