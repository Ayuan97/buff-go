package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	legacyrun "buff-go/internal/buffgo/run"
)

func TestRunMapsOptionsAndSafelyWrapsFailure(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("postgres://user:password@host/database")
	opt := Options{ConfigPath: "config.toml", Sources: "steam.ask", AppID: 252490}
	var got legacyrun.Options
	err := runWith(ctx, opt, func(runCtx context.Context, runOpt legacyrun.Options) error {
		if runCtx != ctx {
			t.Fatal("runtime received a different context")
		}
		got = runOpt
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error does not preserve cause: %v", err)
	}
	want := legacyrun.Options{ConfigPath: opt.ConfigPath, Sources: opt.Sources, AppID: opt.AppID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy options=%+v want %+v", got, want)
	}
	formatted := fmt.Sprintf("%#v", err)
	if strings.Contains(formatted, "password") || !strings.Contains(formatted, "error_ref=") {
		t.Fatalf("unsafe wrapped error: %s", formatted)
	}
}

func TestRunTreatsSignalCancellationAsCleanStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWith(ctx, Options{ConfigPath: "config.toml"}, func(context.Context, legacyrun.Options) error {
		return context.Canceled
	}); err != nil {
		t.Fatalf("canceled runtime returned error: %v", err)
	}
}

func TestRunWaitsForLegacyCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	cleaned := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runWith(ctx, Options{ConfigPath: "config.toml"}, func(runCtx context.Context, _ legacyrun.Options) error {
			close(started)
			<-runCtx.Done()
			close(cleanupStarted)
			<-releaseCleanup
			close(cleaned)
			return runCtx.Err()
		})
	}()

	<-started
	cancel()
	<-cleanupStarted
	select {
	case err := <-done:
		t.Fatalf("app returned before cleanup was released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseCleanup)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("app did not wait for legacy runtime")
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("app returned before legacy cleanup")
	}
}

func TestRunNormalizesLegacySources(t *testing.T) {
	var got string
	err := runWith(context.Background(), Options{
		ConfigPath: "config.toml",
		Sources:    " BUFF.ASK, steam.ask,buff.ask ",
	}, func(_ context.Context, opt legacyrun.Options) error {
		got = opt.Sources
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "buff.ask,steam.ask" {
		t.Fatalf("sources=%q", got)
	}
}

func TestRunRejectsUnsupportedSourcesWithoutEcho(t *testing.T) {
	const secret = "cookie=session-secret"
	called := false
	err := runWith(context.Background(), Options{
		ConfigPath: "config.toml",
		Sources:    secret,
	}, func(context.Context, legacyrun.Options) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("error=%v", err)
	}
	if called {
		t.Fatal("invalid sources reached legacy runtime")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed source value: %v", err)
	}
}

func TestRunRejectsUnsafeAPIListenAddressBeforeRuntime(t *testing.T) {
	called := false
	err := runWithServer(context.Background(), Options{
		ConfigPath: "config.toml",
		APIListen:  "0.0.0.0:8080",
	}, func(context.Context, legacyrun.Options) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrInvalidOptions) || called {
		t.Fatalf("err=%v runtime_called=%v", err, called)
	}
}
