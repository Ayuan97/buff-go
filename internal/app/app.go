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

	listener, err := net.Listen("tcp", opt.APIListen)
	if err != nil {
		return telemetry.WrapError("api listen", err)
	}
	// 成功后必须打一行，否则终端无输出会被当成没启动。
	_, _ = fmt.Fprintf(os.Stderr, "buffgo: ready http://%s\n", listener.Addr().String())
	handler := api.NewHandlerForAuthority(webui.Handler(), opt.APIListen, api.ControlServices{
		Accounts:        accounts,
		Nodes:           nodes,
		Combinations:    combinations,
		Collection:      targets,
		Market:          quotes,
		Providers:       providers,
		PlatformRegions: platformTargetRegions(),
	})
	server := &http.Server{Handler: handler}
	serverDone := make(chan error, 1)
	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		serverDone <- serveErr
	}()

	daemonCtx, daemonCancel := context.WithCancel(context.Background())
	daemonDone := make(chan error, 1)
	go func() {
		runErr := daemon.Run(daemonCtx)
		if errors.Is(runErr, context.Canceled) {
			runErr = nil
		}
		daemonDone <- runErr
	}()

	var serverErr error
	var daemonErr error
	daemonExited := false
	select {
	case serverErr = <-serverDone:
	case daemonErr = <-daemonDone:
		daemonExited = true
	case <-ctx.Done():
	}

	daemonCancel()
	if !daemonExited {
		select {
		case daemonErr = <-daemonDone:
		case <-time.After(35 * time.Second):
			daemonErr = errors.New("collection daemon stop timed out")
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = server.Shutdown(shutdownCtx)
	shutdownCancel()
	if serverErr == nil {
		select {
		case serverErr = <-serverDone:
		case <-time.After(5 * time.Second):
			serverErr = errors.New("api server stop timed out")
		}
	}
	if ctx.Err() != nil {
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

// ErrorRef returns an opaque reference suitable for process error output.
func ErrorRef(err error) string {
	return telemetry.SafeErrorRef(err)
}
