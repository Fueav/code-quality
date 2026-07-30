package membership

type Snapshot struct {
	Version int
	Allowed bool
}

type Authority interface {
	// Version is a cheap coherence read; equal versions identify equal decisions.
	Version(user string) int
	Current(user string) Snapshot
}

type Cache interface {
	Get(user string) (Snapshot, bool)
}

func Authorize(authority Authority, cache Cache, user string) bool {
	version := authority.Version(user)
	if cached, ok := cache.Get(user); ok && cached.Version == version {
		return cached.Allowed
	}
	return authority.Current(user).Allowed
}
