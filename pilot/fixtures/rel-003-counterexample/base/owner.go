package owner

import "sync"

type State struct {
	mutex sync.Mutex
	value int
}

func (state *State) Apply(delta int) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.value += delta
}
