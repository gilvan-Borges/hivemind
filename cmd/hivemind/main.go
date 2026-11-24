package main

import (
	"fmt"
	"os"

	"github.com/gilvan-Borges/HiveMind/internal/browserx"
	"github.com/gilvan-Borges/HiveMind/internal/cli"
)

func main() {
	if browserx.LaunchedAsNativeHost(os.Args) {
		if err := browserx.RunNativeHost(); err != nil {
			fmt.Fprintln(os.Stderr, "erro:", browserx.Redact(err.Error(), ""))
			os.Exit(1)
		}
		return
	}
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}
