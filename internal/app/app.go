package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"buff-go/internal/api"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
	"buff-go/internal/telemetry"
	"buff-go/internal/webui"
)

// ErrInvalidOptions marks startup arguments that must be corrected by the caller.
var ErrInvalidOptions = errors.New("invalid app options")

// Options contains the local API control-plane startup arguments.
type Options struct {
	APIListen   string
	PostgresDSN string
}

type residentDaemon interface {
	RunReady(context.Context, chan<- error) error
}

type listenFunc func(network, address string) (net.Listener, error)

const daemonStopTimeout = 80 * time.Second

// Run starts the local API server with PostgreSQL-backed control services.
func Run(ctx context.Context, opt Options) error {
	if ctx == nil {
		return errors.New("app: context is required")
	}
	if strings.TrimSpace(opt.APIListen) == "" {
		return fmt.Errorf("%w: api listen address is required", ErrInvalidOptions)
	}
	if err := api.ValidateListenAddress(opt.APIListen); err != nil {
		return fmt.Errorf("%w: invalid api listen address", ErrInvalidOptions)
	}
	if strings.TrimSpace(opt.PostgresDSN) == "" {
		return fmt.Errorf("%w: postgres dsn is required", ErrInvalidOptions)
	}

	db, err := openPostgres(ctx, opt.PostgresDSN)
	if err != nil {
		return telemetry.WrapError("postgres connect", err)
	}
	defer func() { _ = db.Close() }()

	if err := postgres.ApplyMigrations(ctx, db); err != nil {
		return telemetry.WrapError("postgres migrate", err)
	}
	store, err := postgres.New(db)
	if err != nil {
		return telemetry.WrapError("postgres store", err)
	}
	if err := seedSteamRateLimits(ctx, store); err != nil {
		return telemetry.WrapError("rate-limit seed", err)
	}
	coordinator, err := resource.NewCoordinator(store)
	if err != nil {
		return telemetry.WrapError("resource coordinator", err)
	}
	accounts, nodes, combinations, targets, quotes, err := newControlServices(store, coordinator)
	if err != nil {
		return err
	}
	providers := providerControl{store: store}
	daemon, err := newCollectionDaemon(store, coordinator)
	if err != nil {
		return telemetry.WrapError("collection daemon", err)
	}
	targets.runtime = daemon
	handler := api.NewHandlerForAuthority(webui.Handler(), opt.APIListen, api.ControlServices{
		Accounts:        accounts,
		Nodes:           nodes,
		Combinations:    combinations,
		Collection:      targets,
		Market:          quotes,
		Providers:       providers,
		PlatformRegions: platformTargetRegions(),
	})
	return runControlPlane(ctx, opt.APIListen, handler, daemon, net.Listen)
}

func runControlPlane(ctx context.Context, address string, handler http.Handler, daemon residentDaemon, listen listenFunc) error {
	daemonCtx, daemonCancel := context.WithCancel(context.Background())
	defer daemonCancel()

	listener, err := listen("tcp", address)
	if err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return telemetry.WrapError("api listen", err)
	}
	listenerClosed := false
	closeListener := func() {
		if listenerClosed {
			return
		}
		listenerClosed = true
		_ = listener.Close()
	}
	defer closeListener()

	if ctx.Err() != nil {
		closeListener()
		return nil
	}

	daemonReady := make(chan error, 1)
	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- daemon.RunReady(daemonCtx, daemonReady)
	}()

	select {
	case readyErr := <-daemonReady:
		if readyErr != nil {
			closeListener()
			daemonCancel()
			if daemonErr := waitDaemonExit(daemonDone); daemonErr != nil {
				readyErr = errors.Join(readyErr, daemonErr)
			}
			return telemetry.WrapError("collection daemon", readyErr)
		}
	case daemonErr := <-daemonDone:
		closeListener()
		daemonCancel()
		if ctx.Err() != nil {
			if daemonErr != nil {
				return telemetry.WrapError("collection daemon", daemonErr)
			}
			return nil
		}
		select {
		case readyErr := <-daemonReady:
			if readyErr != nil {
				return telemetry.WrapError("collection daemon", readyErr)
			}
		default:
		}
		if daemonErr == nil {
			daemonErr = errors.New("collection daemon exited before readiness")
		}
		return telemetry.WrapError("collection daemon", daemonErr)
	case <-ctx.Done():
		closeListener()
		daemonCancel()
		if daemonErr := waitDaemonExit(daemonDone); daemonErr != nil {
			return telemetry.WrapError("collection daemon", daemonErr)
		}
		return nil
	}

	if ctx.Err() != nil {
		closeListener()
		daemonCancel()
		if daemonErr := waitDaemonExit(daemonDone); daemonErr != nil {
			return telemetry.WrapError("collection daemon", daemonErr)
		}
		return nil
	}
	// 成功后必须打一行，否则终端无输出会被当成没启动。
	_, _ = fmt.Fprintf(os.Stderr, "buffgo: ready http://%s\n", listener.Addr().String())
	server := &http.Server{Handler: handler}
	serverDone := make(chan error, 1)
	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) || errors.Is(serveErr, net.ErrClosed) {
			serveErr = nil
		}
		serverDone <- serveErr
	}()

	var serverErr error
	var daemonErr error
	serverExited := false
	daemonExited := false
	select {
	case serverErr = <-serverDone:
		serverExited = true
	case daemonErr = <-daemonDone:
		daemonExited = true
	case <-ctx.Done():
	}

	closeListener()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	shutdownErr := server.Shutdown(shutdownCtx)
	shutdownCancel()
	if errors.Is(shutdownErr, net.ErrClosed) {
		shutdownErr = nil
	}
	if shutdownErr != nil {
		_ = server.Close()
		serverErr = errors.Join(serverErr, shutdownErr)
	}

	// 先关闭 HTTP 入口，再停止并等待采集循环。
	daemonCancel()
	if !daemonExited {
		daemonErr = waitDaemonExit(daemonDone)
	}
	if !serverExited {
		select {
		case serveErr := <-serverDone:
			serverErr = errors.Join(serverErr, serveErr)
		case <-time.After(5 * time.Second):
			serverErr = errors.Join(serverErr, errors.New("api server stop timed out"))
		}
	}
	if ctx.Err() != nil {
		if daemonErr != nil {
			return telemetry.WrapError("collection daemon", daemonErr)
		}
		if serverErr != nil {
			return telemetry.WrapError("api server", serverErr)
		}
		return nil
	}
	if daemonErr != nil {
		return telemetry.WrapError("collection daemon", daemonErr)
	}
	if serverErr != nil {
		return telemetry.WrapError("api server", serverErr)
	}
	return nil
}

func waitDaemonExit(done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(daemonStopTimeout):
		return errors.New("collection daemon stop timed out")
	}
}

// ErrorRef returns an opaque reference suitable for process error output.
func ErrorRef(err error) string {
	return telemetry.SafeErrorRef(err)
}
