package cache

import "sync"

type Cache struct {
	mutex  sync.Mutex
	values map[string]string
}

func (cache *Cache) Set(id, value string) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.values[id] = value
}
