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
	current := authority.Current(user)
	if cached, ok := cache.Get(user); ok && cached.Version == current.Version {
		return cached.Allowed
	}
	return current.Allowed
}
