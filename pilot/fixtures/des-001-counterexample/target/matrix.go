package matrix

var fixedRoles = [...]string{"admin", "member", "auditor", "service"}
var fixedStates = [...]string{"active", "disabled", "pending"}

func PermissionMatrix() map[string]map[string]bool {
	result := make(map[string]map[string]bool, len(fixedRoles))
	for _, role := range fixedRoles {
		result[role] = make(map[string]bool, len(fixedStates))
		for _, state := range fixedStates {
			result[role][state] = role == "admin" || state == "active"
		}
	}
	return result
}
