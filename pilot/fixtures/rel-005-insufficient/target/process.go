package process

import "os"

func Run(operation func() error) {
	if err := operation(); err != nil {
		os.Exit(1)
	}
}
