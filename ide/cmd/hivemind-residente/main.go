package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/shellres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := shellres.Run(ctx, func() (map[string]shellres.Handler, error) {
		mgr := idecore.NewResidentManager(idecore.ResidentManagerOptions{})
		return map[string]shellres.Handler{
			"ResidentStatus": func(context.Context, json.RawMessage) (any, error) {
				return mgr.Status(), nil
			},
		}, nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
