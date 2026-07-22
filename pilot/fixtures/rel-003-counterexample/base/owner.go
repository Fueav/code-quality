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

func (state *State) Close() {}

func Process(deltas []int) {
	state := &State{}
	defer state.Close()
	for _, delta := range deltas {
		state.Apply(delta)
	}
}
