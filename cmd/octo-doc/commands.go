package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/assets"
	"github.com/Mininglamp-OSS/octo-doc/internal/config"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage/postgres"
	s3store "github.com/Mininglamp-OSS/octo-doc/internal/storage/s3"
	"github.com/Mininglamp-OSS/octo-doc/internal/transport/httpx"
)

// buildServices opens the storage backends and constructs the service layer. The
// returned health func pings both stores (readiness probe); closeStore releases
// the pool.
func buildServices(ctx context.Context, cfg *config.Config) (deps *httpx.Deps, closeStore func() error, err error) {
	meta, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.PGPoolMax)
	if err != nil {
		return nil, nil, err
	}
	blobs, err := s3store.Open(ctx, s3store.Options{
		Bucket:         cfg.S3Bucket,
		Region:         cfg.S3Region,
		Endpoint:       cfg.S3Endpoint,
		ForcePathStyle: cfg.S3ForcePathStyle,
		AccessKeyID:    cfg.S3AccessKeyID,
		SecretKey:      cfg.S3SecretKey,
	})
	if err != nil {
		_ = meta.Close()
		return nil, nil, err
	}
	locker := meta.Locker()
	comments := service.NewCommentService(meta, locker)
	docs := service.NewDocService(blobs, meta, comments, locker, cfg.BaseURL, cfg.MaxHTMLBytes)
	auth := service.NewAuthService(meta, cfg, locker)
	health := func(hctx context.Context) error {
		if e := meta.Health(hctx); e != nil {
			return e
		}
		return blobs.Health(hctx)
	}
	return &httpx.Deps{
		Config: cfg, Docs: docs, Comments: comments, Auth: auth, Health: health,
	}, meta.Close, nil
}

func serve(cfg *config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	deps, closeStore, err := buildServices(startCtx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeStore() }()

	deps.Logger = logger
	deps.OverlayJS = assets.OverlayJS
	srv := httpx.New(*deps)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("octo-doc listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-stop:
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func migrate(cfg *config.Config, logger *slog.Logger) error {
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required for migrate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := postgres.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	logger.Info("schema applied")
	return nil
}

func bootstrap(cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	meta, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.PGPoolMax)
	if err != nil {
		return err
	}
	defer func() { _ = meta.Close() }()
	auth := service.NewAuthService(meta, cfg, meta.Locker())
	token, err := auth.Bootstrap(ctx)
	if err != nil {
		return err
	}
	_, _ = io.WriteString(os.Stdout, token+"\n")
	return nil
}
