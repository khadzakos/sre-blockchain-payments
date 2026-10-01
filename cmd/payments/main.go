package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/khadzakos/sre-blockchain-payments/internal/config"
	"github.com/khadzakos/sre-blockchain-payments/internal/handler"
	"github.com/khadzakos/sre-blockchain-payments/internal/repository"
	"github.com/khadzakos/sre-blockchain-payments/internal/service"
	"github.com/khadzakos/sre-blockchain-payments/internal/telemetry"
	"github.com/khadzakos/sre-blockchain-payments/internal/worker"
)

var version = "dev"

func main() {
	bootstrap, _ := telemetry.NewLogger("info", "startup", version)
	if err := run(); err != nil {
		zap.L().Error("application stopped", zap.Error(err))
		_ = zap.L().Sync()
		os.Exit(1)
	}
	_ = bootstrap.Sync()
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: payments api|worker|migrate|version")
	}
	command := os.Args[1]
	if command == "version" {
		fmt.Println(version)
		return nil
	}
	if command != "api" && command != "worker" && command != "migrate" {
		return fmt.Errorf("unknown command")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	log, err := telemetry.NewLogger(c.LogLevel, command, version)
	if err != nil {
		return fmt.Errorf("invalid log configuration: %w", err)
	}
	defer func() { _ = log.Sync() }()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.InitTracing(ctx, "payments-"+command, version, c.TraceEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(flush); err != nil {
			log.Warn("trace shutdown failed", zap.Error(err))
		}
	}()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	repo, err := repository.New(startup, c.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer repo.Close()
	if command == "migrate" {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		ctx, finish := telemetry.Start(ctx, "admin.migrate")
		err = repo.Migrate(ctx)
		finish(err)
		if err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	}
	s := service.New(repo)
	metrics := telemetry.NewMetrics()
	var h http.Handler
	processCtx, cancelProcess := context.WithCancel(ctx)
	defer cancelProcess()
	workerDone := make(chan error, 1)
	if command == "worker" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", metrics.Handler())
		h = mux
		go func() { workerDone <- worker.Run(processCtx, s, c.WorkerInterval, metrics) }()
	} else {
		h = handler.New(s, log, metrics)
	}
	server := &http.Server{
		Addr:              ":" + c.Port,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	failures := make(chan error, 1)
	go func() {
		log.Info("http server started", zap.String("port", c.Port))
		failures <- server.ListenAndServe()
	}()
	var serveErr error
	workerFinished := false
	select {
	case serveErr = <-failures:
	case <-ctx.Done():
	case serveErr = <-workerDone:
		workerFinished = true
	}
	cancelProcess()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down")
	if err = server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return err
	}
	if command == "worker" && !workerFinished {
		select {
		case <-workerDone:
		case <-shutdown.Done():
			return shutdown.Err()
		}
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}
