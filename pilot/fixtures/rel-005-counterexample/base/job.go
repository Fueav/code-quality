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

func (supervisor *Supervisor) Run(probe func() error) error {
	return probe()
}

func Run(supervisor *Supervisor, probe func() error) error {
	return supervisor.Run(probe)
}
