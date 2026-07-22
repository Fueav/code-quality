package owner

import "sync"

type event struct {
	delta int
	done  chan struct{}
}

type State struct {
	once   sync.Once
	mu     sync.RWMutex
	closed bool
	events chan event
}

func (state *State) start() {
	state.events = make(chan event)
	go func(events <-chan event) {
		value := 0
		for next := range events {
			value += next.delta
			close(next.done)
		}
	}(state.events)
}

func (state *State) Apply(delta int) {
	state.once.Do(state.start)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.closed {
		return
	}
	done := make(chan struct{})
	state.events <- event{delta: delta, done: done}
	<-done
}

func (state *State) Close() {
	state.once.Do(state.start)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	state.closed = true
	close(state.events)
}

func Process(deltas []int) {
	state := &State{}
	defer state.Close()
	for _, delta := range deltas {
		state.Apply(delta)
	}
}
