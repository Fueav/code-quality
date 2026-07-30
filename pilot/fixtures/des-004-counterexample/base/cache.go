package membership

type Snapshot struct {
	Version int
	Allowed bool
}

type Authority interface {
	Current(user string) Snapshot
}

type Cache interface {
	Get(user string) (Snapshot, bool)
}

func Authorize(authority Authority, cache Cache, user string) bool {
	return authority.Current(user).Allowed
}
