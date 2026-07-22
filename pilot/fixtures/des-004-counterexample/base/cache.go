package membership

type Snapshot struct {
	Version int
	Allowed bool
}

type Authority interface {
	Current(user string) Snapshot
}

func Authorize(authority Authority, user string) bool {
	return authority.Current(user).Allowed
}
