package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"buff-go/internal/app"
)

func TestExecuteSignalCancellationWaitsForCleanup(t *testing.T) {
	var (
		mu     sync.Mutex
		events []string
	)
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	started := make(chan struct{})
	var cancel context.CancelFunc
	notify := func(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
		want := []os.Signal{os.Interrupt, syscall.SIGTERM}
		if !reflect.DeepEqual(signals, want) {
			t.Fatalf("signals=%v want %v", signals, want)
		}
		ctx, cancelContext := context.WithCancel(parent)
		cancel = cancelContext
		return ctx, func() {
			record("signal_stop")
			cancelContext()
		}
	}
	run := func(ctx context.Context, opt app.Options) error {
		if opt.APIListen != "127.0.0.1:18080" || opt.PostgresDSN != "postgres://local/buffgo" {
			t.Fatalf("options=%+v", opt)
		}
		close(started)
		<-ctx.Done()
		record("runtime_cleanup")
		return nil
	}
	done := make(chan int, 1)
	go func() {
		done <- execute([]string{"-api-listen", "127.0.0.1:18080", "-pg-dsn", "postgres://local/buffgo"}, &bytes.Buffer{}, notify, run)
	}()

	<-started
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code=%d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("execute did not wait for runtime cleanup")
	}

	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	if want := []string{"runtime_cleanup", "signal_stop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%v want %v", got, want)
	}
}

func TestExecuteStartupFailure(t *testing.T) {
	const secret = "postgres://user:password@host/database"
	var stderr bytes.Buffer
	stopped := false
	notify := func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		return ctx, func() {
			stopped = true
			cancel()
		}
	}
	code := execute([]string{"-api-listen", "127.0.0.1:18080", "-pg-dsn", "postgres://local/buffgo"}, &stderr, notify, func(context.Context, app.Options) error {
		return errors.New(secret)
	})
	if code != 1 {
		t.Fatalf("exit code=%d", code)
	}
	if !stopped {
		t.Fatal("signal context was not stopped")
	}
	if strings.Contains(stderr.String(), secret) || !strings.Contains(stderr.String(), "error_ref=") {
		t.Fatalf("unsafe stderr=%s", stderr.String())
	}
}

func TestExecuteParseErrorsDoNotEchoArguments(t *testing.T) {
	tests := [][]string{
		{"-api-listen", "127.0.0.1:18080", "-cookie", "session-secret\nsecond-line"},
	}
	for _, args := range tests {
		var stderr bytes.Buffer
		code := execute(args, &stderr, func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
			t.Fatal("parse error installed signal handlers")
			return nil, nil
		}, func(context.Context, app.Options) error {
			t.Fatal("parse error started runtime")
			return nil
		})
		if code != 2 {
			t.Fatalf("args=%v exit code=%d", args, code)
		}
		output := stderr.String()
		for _, secret := range []string{"session-secret", "second-line"} {
			if strings.Contains(output, secret) {
				t.Fatalf("parse error echoed %q: %s", secret, output)
			}
		}
		if output != "buffgo: invalid arguments\n" {
			t.Fatalf("parse output=%q", output)
		}
	}
}

func TestExecuteArgumentExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "help", args: []string{"-h"}, want: 0},
		{name: "empty api listen equals", args: []string{"-api-listen="}, want: 2},
		{name: "unknown legacy config flag", args: []string{"-config", "runtime.toml"}, want: 2},
		{name: "positional argument", args: []string{"-api-listen", "127.0.0.1:18080", "-pg-dsn", "postgres://local/buffgo", "extra"}, want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := execute(tc.args, &stderr, func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
				t.Fatal("invalid arguments installed signal handlers")
				return nil, nil
			}, func(context.Context, app.Options) error {
				t.Fatal("invalid arguments started runtime")
				return nil
			})
			if code != tc.want {
				t.Fatalf("exit code=%d want %d stderr=%s", code, tc.want, stderr.String())
			}
			for _, secret := range []string{"runtime.toml", "password", "session-secret"} {
				if strings.Contains(stderr.String(), secret) {
					t.Fatalf("stderr echoed %q: %s", secret, stderr.String())
				}
			}
		})
	}
}

func TestExecuteUsesLocalDefaults(t *testing.T) {
	t.Setenv("BUFFGO_DSN", "")
	var got app.Options
	code := execute(nil, &bytes.Buffer{}, func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}, func(_ context.Context, opt app.Options) error {
		got = opt
		return nil
	})
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if got.APIListen != defaultAPIListen || got.PostgresDSN != defaultPostgresDSN {
		t.Fatalf("options=%+v", got)
	}
}

func TestExecuteRejectsUnsafeListenViaApp(t *testing.T) {
	tests := []struct {
		name  string
		value string
		echo  string
	}{
		{name: "wildcard", value: "0.0.0.0:8080", echo: "0.0.0.0"},
		{name: "hostname", value: "localhost:8080", echo: "localhost"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := execute([]string{"-api-listen", tc.value, "-pg-dsn", "postgres://local/buffgo"}, &stderr,
				func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
					return context.WithCancel(parent)
				}, app.Run)
			if code != 2 {
				t.Fatalf("exit code=%d stderr=%s", code, stderr.String())
			}
			if strings.Contains(stderr.String(), tc.echo) {
				t.Fatalf("stderr echoed listen address: %s", stderr.String())
			}
		})
	}
}
