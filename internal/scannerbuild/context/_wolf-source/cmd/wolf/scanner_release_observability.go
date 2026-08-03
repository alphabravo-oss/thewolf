package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

func configureScannerReleaseComponents(
	registry *scannerobservability.Registry,
	role string,
) {
	components := map[scannerobservability.Component]string{
		scannerobservability.ComponentAlert:        "alert",
		scannerobservability.ComponentBuild:        "build",
		scannerobservability.ComponentDiscovery:    "discovery",
		scannerobservability.ComponentNotification: "notification",
		scannerobservability.ComponentProposal:     "proposal",
		scannerobservability.ComponentRegistry:     "registry",
		scannerobservability.ComponentRollout:      "rollout",
		scannerobservability.ComponentScheduler:    "scheduler",
	}
	for component, componentRole := range components {
		enabled := role == "all" || role == componentRole
		registry.Enable(component, enabled)
		if enabled {
			registry.SetState(component, "active")
		}
	}
}

func serveScannerReleaseObservability(
	ctx context.Context,
	address string,
	registry *scannerobservability.Registry,
) (func(), error) {
	address = strings.TrimSpace(address)
	if address == "" || strings.EqualFold(address, "off") {
		return func() {}, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for scanner release observability on %q: %w", address, err)
	}
	server := &http.Server{
		Handler:           registry.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil &&
			!errors.Is(serveErr, http.ErrServerClosed) {
			wolflog.Error().Err(serveErr).Msg("scanner release observability server stopped")
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownContext)
		})
	}, nil
}
