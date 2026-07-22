package job

import "time"

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
	var err error
	for attempt := 0; attempt <= supervisor.maxRestarts; attempt++ {
		if err = probe(); err == nil {
			return nil
		}
		if attempt < supervisor.maxRestarts {
			time.Sleep(time.Duration(1<<attempt) * time.Millisecond)
		}
	}
	if supervisor.pager != nil {
		supervisor.pager.Page(err)
	}
	return err
}

func Run(supervisor *Supervisor, probe func() error) error {
	return supervisor.Run(probe)
}
