package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"buff-go/internal/app"
)

type appRunFunc func(context.Context, app.Options) error
type notifyContextFunc func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)

func main() {
	os.Exit(execute(os.Args[1:], os.Stderr, signal.NotifyContext, app.Run))
}

func execute(args []string, stderr io.Writer, notify notifyContextFunc, run appRunFunc) int {
	if stderr == nil {
		stderr = io.Discard
	}
	var flagOutput bytes.Buffer
	flags := flag.NewFlagSet("buffgo", flag.ContinueOnError)
	flags.SetOutput(&flagOutput)
	apiListen := flags.String("api-listen", "", "loopback API listen address, for example 127.0.0.1:8080")
	pgDSN := flags.String("pg-dsn", "", "PostgreSQL connection string")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.Copy(stderr, &flagOutput)
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "buffgo: invalid arguments")
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "buffgo: positional arguments are not supported")
		return 2
	}
	if strings.TrimSpace(*apiListen) == "" {
		_, _ = fmt.Fprintln(stderr, "buffgo: -api-listen is required")
		return 2
	}
	if strings.TrimSpace(*pgDSN) == "" {
		*pgDSN = strings.TrimSpace(os.Getenv("BUFFGO_DSN"))
	}
	if strings.TrimSpace(*pgDSN) == "" {
		_, _ = fmt.Fprintln(stderr, "buffgo: -pg-dsn is required")
		return 2
	}

	ctx, stop := notify(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, app.Options{APIListen: *apiListen, PostgresDSN: *pgDSN}); err != nil {
		if errors.Is(err, app.ErrInvalidOptions) {
			_, _ = fmt.Fprintf(stderr, "buffgo: %v\n", err)
			return 2
		}
		_, _ = fmt.Fprintf(stderr, "buffgo: startup failed error_ref=%s\n", app.ErrorRef(err))
		return 1
	}
	return 0
}
