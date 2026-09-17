// Command frost recommends a model and execution profile for a task. It
// never launches the task.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/marcus/frost/internal/cli"
	"github.com/marcus/frost/pkg/buildinfo"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	home, _ := os.UserHomeDir()
	return cli.Run(cli.Env{
		Args:    os.Args[1:],
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Context: ctx,
		Getenv:  os.Getenv,
		Home:    home,
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
	})
}
