package cache

type Cache struct {
	values map[string]string
}

func (cache *Cache) Set(id, value string) {
	cache.values[id] = value
}
