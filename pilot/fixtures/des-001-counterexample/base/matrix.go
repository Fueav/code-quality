package matrix

var permitted = map[string]map[string]bool{
	"admin":  {"active": true, "disabled": false},
	"member": {"active": true, "disabled": false},
}

func IsPermitted(role, state string) bool {
	return permitted[role][state]
}
