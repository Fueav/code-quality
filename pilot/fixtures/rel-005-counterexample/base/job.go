package job

type Pager interface {
	Page(error)
}

type Supervisor struct {
	maxRestarts int
	pager       Pager
}

func NewSupervisor(maxRestarts int, pager Pager) *Supervisor {
	return &Supervisor{maxRestarts: maxRestarts, pager: pager}
}

func (supervisor *Supervisor) Run(operation func() error) error {
	return operation()
}

func Run(supervisor *Supervisor, operation func() error) error {
	return supervisor.Run(operation)
}
