package matrix

var fixedRoles = [...]string{"admin", "member"}
var fixedStates = [...]string{"active", "disabled"}

func IsPermitted(role, state string) bool {
	for _, candidateRole := range fixedRoles {
		for _, candidateState := range fixedStates {
			if role == candidateRole && state == candidateState {
				return candidateState == "active"
			}
		}
	}
	return false
}
