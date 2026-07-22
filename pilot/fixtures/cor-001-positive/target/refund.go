package main

import "os"

const day = 24

func RefundAllowed(ageHours int) bool {
	return ageHours < 7*day
}

func HandleRefund(ageHours int) bool {
	return RefundAllowed(ageHours)
}

func main() {
	if !HandleRefund(20 * day) {
		os.Exit(2)
	}
}
