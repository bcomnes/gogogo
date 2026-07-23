package main

import (
	"context"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(newApplication().run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
