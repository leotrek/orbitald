package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/orbitald/orbitald/internal/orbitald"
)

func main() {
	cfg := orbitald.ConfigFromFlags()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := orbitald.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
