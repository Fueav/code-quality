package process

import "log"

func Run(operation func() error) {
	if err := operation(); err != nil {
		log.Print(err)
	}
}
