package job

import "time"

type Supervisor struct {
	maxRestarts int
	paged       bool
}

func (supervisor *Supervisor) Run(operation func() error) error {
	var err error
	for attempt := 0; attempt <= supervisor.maxRestarts; attempt++ {
		if err = operation(); err == nil {
			return nil
		}
		if attempt < supervisor.maxRestarts {
			time.Sleep(time.Duration(1<<attempt) * time.Millisecond)
		}
	}
	supervisor.paged = true
	return err
}

func Run(supervisor *Supervisor, operation func() error) error {
	return supervisor.Run(operation)
}
