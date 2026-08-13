package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunRequiresAPIListen(t *testing.T) {
	err := Run(context.Background(), Options{PostgresDSN: "postgres://local/buffgo"})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), "0.0.0.0") {
		t.Fatalf("unexpected echo: %v", err)
	}
}

func TestRunRejectsUnsafeAPIListenAddress(t *testing.T) {
	for _, value := range []string{"0.0.0.0:8080", "localhost:8080", "example.com:8080"} {
		err := Run(context.Background(), Options{APIListen: value})
		if !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("value=%q error=%v", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error echoed listen address: %v", err)
		}
	}
}

func TestRunRequiresPostgresDSN(t *testing.T) {
	err := Run(context.Background(), Options{APIListen: "127.0.0.1:18080"})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("error=%v", err)
	}
}

func TestRunServesUntilCancelled(t *testing.T) {
	dsn := os.Getenv("BUFFGO_TEST_DSN")
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN to an isolated PostgreSQL database")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{APIListen: addr, PostgresDSN: dsn}) }()

	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, getErr := http.Get("http://" + addr + "/api/security/context")
		if getErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				lastErr = nil
				break
			}
			lastErr = fmt.Errorf("status=%d", resp.StatusCode)
		} else {
			lastErr = getErr
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		cancel()
		<-done
		t.Fatalf("api not ready: %v", lastErr)
	}

	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("app did not stop after cancel")
	}
}

func TestRunWrapsListenFailureSafely(t *testing.T) {
	dsn := os.Getenv("BUFFGO_TEST_DSN")
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN to an isolated PostgreSQL database")
	}
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()

	err = Run(context.Background(), Options{APIListen: blocker.Addr().String(), PostgresDSN: dsn})
	if err == nil {
		t.Fatal("expected listen failure")
	}
	if errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("listen failure mapped to invalid options: %v", err)
	}
	formatted := fmt.Sprintf("%#v", err)
	if !strings.Contains(formatted, "error_ref=") {
		t.Fatalf("unsafe wrapped error: %s", formatted)
	}
}

func TestErrorRefOpaque(t *testing.T) {
	ref := ErrorRef(errors.New("postgres://user:password@host/db"))
	if strings.Contains(ref, "password") || ref == "" {
		t.Fatalf("ref=%q", ref)
	}
}
