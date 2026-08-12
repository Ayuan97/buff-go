// Package app owns process dependency assembly and the legacy runtime bridge.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"buff-go/internal/api"
	legacyrun "buff-go/internal/buffgo/run"
	"buff-go/internal/telemetry"
)

// ErrInvalidOptions marks startup arguments that must be corrected by the caller.
var ErrInvalidOptions = errors.New("invalid app options")

const (
	legacySteamSell = "steam.ask"
	legacyBuffSell  = "buff.ask"
)

// Options contains the temporary legacy runtime startup arguments.
type Options struct {
	ConfigPath string
	Sources    string
	AppID      int64
	APIListen  string
}

type legacyRunFunc func(context.Context, legacyrun.Options) error

// Run starts the current runtime bridge and waits until it has stopped.
func Run(ctx context.Context, opt Options) error {
	return runWithServer(ctx, opt, legacyrun.Run)
}

// ErrorRef returns an opaque reference suitable for process error output.
func ErrorRef(err error) string {
	return telemetry.SafeErrorRef(err)
}

func runWith(ctx context.Context, opt Options, run legacyRunFunc) error {
	if err := validateOptions(ctx, opt, run); err != nil {
		return err
	}
	sources, _ := normalizeSources(opt.Sources)
	err := run(ctx, optToLegacy(opt, sources))
	if err == nil {
		return nil
	}
	if ctx.Err() == context.Canceled && errors.Is(err, context.Canceled) {
		return nil
	}
	return telemetry.WrapError("legacy runtime", err)
}

func runWithServer(ctx context.Context, opt Options, run legacyRunFunc) error {
	if strings.TrimSpace(opt.APIListen) == "" {
		return runWith(ctx, opt, run)
	}
	if err := validateOptions(ctx, opt, run); err != nil {
		return err
	}
	if err := api.ValidateListenAddress(opt.APIListen); err != nil {
		return fmt.Errorf("%w: invalid api listen address", ErrInvalidOptions)
	}
	sources, _ := normalizeSources(opt.Sources)
	listener, err := net.Listen("tcp", opt.APIListen)
	if err != nil {
		return telemetry.WrapError("api listen", err)
	}
	server := &http.Server{Handler: api.NewHandlerForAuthority(nil, opt.APIListen)}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverDone <- err
	}()
	legacyDone := make(chan error, 1)
	go func() { legacyDone <- run(runCtx, optToLegacy(opt, sources)) }()

	var legacyErr, serverErr error
	serverExited := false
	select {
	case legacyErr = <-legacyDone:
		cancel()
	case serverErr = <-serverDone:
		serverExited = true
		cancel()
		legacyErr = <-legacyDone
	case <-ctx.Done():
		cancel()
		legacyErr = <-legacyDone
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = server.Shutdown(shutdownCtx)
	shutdownCancel()
	if !serverExited {
		serverErr = <-serverDone
	}
	if legacyErr == nil && serverErr == nil {
		return nil
	}
	if legacyErr != nil && !(ctx.Err() == context.Canceled && errors.Is(legacyErr, context.Canceled)) {
		return telemetry.WrapError("legacy runtime", legacyErr)
	}
	if serverErr != nil {
		return telemetry.WrapError("api server", serverErr)
	}
	return nil
}

func validateOptions(ctx context.Context, opt Options, run legacyRunFunc) error {
	if ctx == nil {
		return errors.New("app: context is required")
	}
	if run == nil {
		return errors.New("app: runtime is required")
	}
	if strings.TrimSpace(opt.ConfigPath) == "" {
		return fmt.Errorf("%w: config path is required", ErrInvalidOptions)
	}
	if opt.AppID < 0 {
		return fmt.Errorf("%w: appid cannot be negative", ErrInvalidOptions)
	}
	if _, err := normalizeSources(opt.Sources); err != nil {
		return err
	}
	return nil
}

func optToLegacy(opt Options, sources string) legacyrun.Options {
	return legacyrun.Options{
		ConfigPath: opt.ConfigPath,
		Sources:    sources,
		AppID:      opt.AppID,
	}
}

func normalizeSources(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	allowed := map[string]struct{}{
		legacySteamSell: {},
		legacyBuffSell:  {},
	}
	seen := make(map[string]struct{}, len(allowed))
	parts := make([]string, 0, len(allowed))
	for _, raw := range strings.Split(value, ",") {
		token := strings.ToLower(strings.TrimSpace(raw))
		if _, ok := allowed[token]; !ok {
			return "", fmt.Errorf("%w: sources contains an unsupported value", ErrInvalidOptions)
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		parts = append(parts, token)
	}
	return strings.Join(parts, ","), nil
}
