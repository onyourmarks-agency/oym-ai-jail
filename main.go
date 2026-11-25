package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"

	"github.com/onyourmarks-agency/oym-ai-jail/cmd/jail"
)

func main() {
	if err := jail.New().Execute(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Println(err.Error())
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
