package owner

type event struct {
	delta int
	done  chan struct{}
}

type Owner struct {
	events chan event
}

func New() *Owner {
	owner := &Owner{events: make(chan event)}
	go func() {
		state := 0
		for next := range owner.events {
			state += next.delta
			close(next.done)
		}
	}()
	return owner
}

func (owner *Owner) Apply(delta int) {
	done := make(chan struct{})
	owner.events <- event{delta: delta, done: done}
	<-done
}
