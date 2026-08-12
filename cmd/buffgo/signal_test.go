//go:build !windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"buff-go/internal/app"
)

func TestExecuteHandlesRealSignals(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
	}{
		{name: "interrupt", signal: os.Interrupt},
		{name: "terminate", signal: syscall.SIGTERM},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestExecuteSignalHelper$")
			cmd.Env = append(os.Environ(), "BUFFGO_SIGNAL_HELPER=1")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			finished := false
			defer func() {
				if !finished {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()

			lines := make(chan string, 2)
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
				close(lines)
			}()
			waitSignalLine(t, lines, "ready")
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			waitSignalLine(t, lines, "cleaned")

			wait := make(chan error, 1)
			go func() { wait <- cmd.Wait() }()
			select {
			case err := <-wait:
				finished = true
				if err != nil {
					t.Fatalf("helper exit: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("helper did not exit after cleanup")
			}
		})
	}
}

func TestExecuteSignalHelper(t *testing.T) {
	if os.Getenv("BUFFGO_SIGNAL_HELPER") != "1" {
		return
	}
	code := execute([]string{"-config", "unused.toml"}, os.Stderr, signal.NotifyContext,
		func(ctx context.Context, _ app.Options) error {
			_, _ = fmt.Fprintln(os.Stdout, "ready")
			<-ctx.Done()
			_, _ = fmt.Fprintln(os.Stdout, "cleaned")
			return nil
		})
	os.Exit(code)
}

func waitSignalLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	select {
	case got, ok := <-lines:
		if !ok {
			t.Fatalf("stdout closed before %q", want)
		}
		if got != want {
			t.Fatalf("line=%q want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %q", want)
	}
}
