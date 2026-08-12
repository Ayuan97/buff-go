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
	configPath := flags.String("config", "", "path to the runtime configuration file")
	sources := flags.String("sources", "", "comma-separated source allowlist")
	appid := flags.Int64("appid", 0, "restrict collection to one appid")
	apiListen := flags.String("api-listen", "", "optional loopback API listen address, for example 127.0.0.1:8080")
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
	if strings.TrimSpace(*configPath) == "" {
		_, _ = fmt.Fprintln(stderr, "buffgo: -config is required")
		return 2
	}
	if *appid < 0 {
		_, _ = fmt.Fprintln(stderr, "buffgo: -appid cannot be negative")
		return 2
	}

	ctx, stop := notify(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, app.Options{ConfigPath: *configPath, Sources: *sources, AppID: *appid, APIListen: *apiListen}); err != nil {
		if errors.Is(err, app.ErrInvalidOptions) {
			_, _ = fmt.Fprintf(stderr, "buffgo: %v\n", err)
			return 2
		}
		_, _ = fmt.Fprintf(stderr, "buffgo: startup failed error_ref=%s\n", app.ErrorRef(err))
		return 1
	}
	return 0
}
