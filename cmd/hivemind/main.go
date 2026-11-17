package main

import (
	"fmt"
	"os"

	"github.com/gilvan-Borges/HiveMind/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}
