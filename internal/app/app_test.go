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
	"sync"
	"testing"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/telemetry"
)

type fakeResidentDaemon struct {
	started             chan struct{}
	allowReady          chan struct{}
	readyErr            error
	cancelErr           error
	canceled            chan struct{}
	listenerClosed      func() bool
	canceledAfterClosed bool
	mu                  sync.Mutex
}

func newFakeResidentDaemon() *fakeResidentDaemon {
	return &fakeResidentDaemon{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

func (daemon *fakeResidentDaemon) RunReady(ctx context.Context, ready chan<- error) error {
	close(daemon.started)
	if daemon.allowReady != nil {
		select {
		case <-daemon.allowReady:
		case <-ctx.Done():
			daemon.recordCancellation()
			return nil
		}
	}
	ready <- daemon.readyErr
	if daemon.readyErr != nil {
		return daemon.readyErr
	}
	<-ctx.Done()
	daemon.recordCancellation()
	return daemon.cancelErr
}

func (daemon *fakeResidentDaemon) recordCancellation() {
	daemon.mu.Lock()
	if daemon.listenerClosed != nil {
		daemon.canceledAfterClosed = daemon.listenerClosed()
	}
	daemon.mu.Unlock()
	close(daemon.canceled)
}

func (daemon *fakeResidentDaemon) wasCanceledAfterListenerClosed() bool {
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	return daemon.canceledAfterClosed
}

type closeTrackingListener struct {
	net.Listener
	acceptCalled chan struct{}
	acceptOnce   sync.Once
	mu           sync.Mutex
	closed       bool
}

func (listener *closeTrackingListener) Accept() (net.Conn, error) {
	if listener.acceptCalled != nil {
		listener.acceptOnce.Do(func() { close(listener.acceptCalled) })
	}
	return listener.Listener.Accept()
}

func (listener *closeTrackingListener) Close() error {
	listener.mu.Lock()
	listener.closed = true
	listener.mu.Unlock()
	return listener.Listener.Close()
}

func (listener *closeTrackingListener) isClosed() bool {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	return listener.closed
}

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

func TestRunControlPlaneBindsBeforeDaemonReady(t *testing.T) {
	daemon := newFakeResidentDaemon()
	daemon.allowReady = make(chan struct{})
	listenerBound := make(chan *closeTrackingListener, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runControlPlane(ctx, "127.0.0.1:0", http.NotFoundHandler(), daemon,
			func(network, address string) (net.Listener, error) {
				base, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					return nil, err
				}
				listener := &closeTrackingListener{
					Listener:     base,
					acceptCalled: make(chan struct{}),
				}
				listenerBound <- listener
				return listener, nil
			})
	}()
	listener := <-listenerBound
	<-daemon.started
	select {
	case <-listener.acceptCalled:
		t.Fatal("API server started before daemon became ready")
	case <-time.After(50 * time.Millisecond):
	}

	close(daemon.allowReady)
	select {
	case <-listener.acceptCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("API server did not start after daemon became ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunControlPlaneClosesListenerWhenDaemonStartupFails(t *testing.T) {
	daemon := newFakeResidentDaemon()
	daemon.readyErr = collection.ErrInstanceLocked
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &closeTrackingListener{Listener: base}
	err = runControlPlane(context.Background(), "127.0.0.1:0", http.NotFoundHandler(), daemon,
		func(network, address string) (net.Listener, error) { return listener, nil })
	if !errors.Is(err, collection.ErrInstanceLocked) {
		t.Fatalf("error = %v", err)
	}
	if !listener.isClosed() {
		t.Fatal("API listener was not closed after daemon startup failed")
	}
}

func TestRunControlPlaneDoesNotStartDaemonWhenListenFails(t *testing.T) {
	daemon := newFakeResidentDaemon()
	listenErr := errors.New("listen failed")
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &closeTrackingListener{Listener: base}
	err = runControlPlane(context.Background(), "127.0.0.1:0", http.NotFoundHandler(), daemon,
		func(network, address string) (net.Listener, error) { return listener, listenErr })
	if !errors.Is(err, listenErr) {
		t.Fatalf("error = %v", err)
	}
	if !listener.isClosed() {
		t.Fatal("listener was not closed after listen failed")
	}
	select {
	case <-daemon.canceled:
		t.Fatal("daemon started before listener succeeded")
	case <-daemon.started:
		t.Fatal("daemon started before listener succeeded")
	default:
	}
}

func TestRunControlPlaneStopsHTTPBeforeCancelingDaemon(t *testing.T) {
	daemon := newFakeResidentDaemon()
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &closeTrackingListener{Listener: base, acceptCalled: make(chan struct{})}
	daemon.listenerClosed = listener.isClosed
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	listenCalled := make(chan struct{})
	go func() {
		done <- runControlPlane(ctx, "127.0.0.1:0", http.NotFoundHandler(), daemon,
			func(network, address string) (net.Listener, error) {
				close(listenCalled)
				return listener, nil
			})
	}()
	<-listenCalled
	select {
	case <-listener.acceptCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("API server did not start")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !daemon.wasCanceledAfterListenerClosed() {
		t.Fatal("daemon was canceled while the HTTP listener still accepted requests")
	}
}

func TestRunControlPlaneCancellationBeforeReadyIsClean(t *testing.T) {
	daemon := newFakeResidentDaemon()
	daemon.allowReady = make(chan struct{})
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &closeTrackingListener{Listener: base}
	daemon.listenerClosed = listener.isClosed
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runControlPlane(ctx, "127.0.0.1:0", http.NotFoundHandler(), daemon,
			func(network, address string) (net.Listener, error) { return listener, nil })
	}()
	<-daemon.started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !daemon.wasCanceledAfterListenerClosed() {
		t.Fatal("daemon was canceled before the unserved listener closed")
	}
}

func TestRunControlPlaneReportsDaemonFailureDuringCancellation(t *testing.T) {
	daemon := newFakeResidentDaemon()
	daemonErr := errors.New("instance lock is gone")
	daemon.cancelErr = daemonErr
	ctx, cancel := context.WithCancel(context.Background())
	listenerReady := make(chan *closeTrackingListener, 1)
	done := make(chan error, 1)
	go func() {
		done <- runControlPlane(ctx, "127.0.0.1:0", http.NotFoundHandler(), daemon,
			func(network, address string) (net.Listener, error) {
				base, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					return nil, err
				}
				listener := &closeTrackingListener{
					Listener:     base,
					acceptCalled: make(chan struct{}),
				}
				listenerReady <- listener
				return listener, nil
			})
	}()
	listener := <-listenerReady
	select {
	case <-listener.acceptCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("API server did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, daemonErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestCollectionDaemonObserversRecordSafeFailures(t *testing.T) {
	recorder := telemetry.NewMemory(4)
	cycleObserver, recoveryObserver := collectionDaemonObservers(recorder)
	secret := "postgres://user:password@host/db"
	cycleObserver(collection.DaemonCycle{
		Err: errors.New(secret),
		Report: collection.CycleReport{
			Targets: []collection.TargetOutcome{{Err: errors.New(secret)}},
			Workers: []collection.WorkerOutcome{{Err: errors.New(secret)}},
		},
	})
	recoveryObserver(collection.RecoveryReport{Failures: errors.New(secret)})
	cycleObserver(collection.DaemonCycle{})
	recoveryObserver(collection.RecoveryReport{})

	events := recorder.Events()
	if len(events) != 4 {
		t.Fatalf("events = %+v", events)
	}
	for _, event := range events {
		if event.Detail == "" || strings.Contains(event.Detail, "password") {
			t.Fatalf("unsafe daemon event = %+v", event)
		}
	}
}
