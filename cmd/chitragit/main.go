// Command chitragit is the keeper of your Git archive.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/TTox-Tech/chitragit/internal/ui/cli"
)

func main() {
	// First Ctrl+C cancels the context so work can stop gracefully;
	// stop() restores default handling so a second Ctrl+C exits at once.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
