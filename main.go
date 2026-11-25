package main

import (
	"os"

	"github.com/yllix/stowr/v3/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
