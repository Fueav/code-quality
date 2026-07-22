package decision

func Allow(status string) bool {
	return status == "pending" || status == "approved"
}
