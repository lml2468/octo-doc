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
	"github.com/Mininglamp-OSS/octo-doc/internal/platform/sluglock"
	"github.com/Mininglamp-OSS/octo-doc/internal/service"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage/postgres"
	s3store "github.com/Mininglamp-OSS/octo-doc/internal/storage/s3"
	"github.com/Mininglamp-OSS/octo-doc/internal/transport/httpx"
)

// buildServices opens the storage backends and constructs the service layer.
func buildServices(ctx context.Context, cfg *config.Config) (*service.DocService, *service.CommentService, *service.AuthService, func() error, error) {
	meta, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.PGPoolMax)
	if err != nil {
		return nil, nil, nil, nil, err
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
		return nil, nil, nil, nil, err
	}
	locker := sluglock.NewMemory()
	comments := service.NewCommentService(meta, locker)
	docs := service.NewDocService(blobs, meta, comments, cfg.BaseURL, cfg.MaxHTMLBytes)
	auth := service.NewAuthService(meta, cfg)
	return docs, comments, auth, meta.Close, nil
}

func serve(cfg *config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx := context.Background()
	docs, comments, auth, closeStore, err := buildServices(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeStore() }()

	srv := httpx.New(httpx.Deps{
		Config: cfg, Logger: logger, Docs: docs, Comments: comments, Auth: auth,
		OverlayJS: assets.OverlayJS,
	})

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
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
	ctx := context.Background()
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
	ctx := context.Background()
	meta, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.PGPoolMax)
	if err != nil {
		return err
	}
	defer func() { _ = meta.Close() }()
	auth := service.NewAuthService(meta, cfg)
	token, err := auth.Bootstrap(ctx)
	if err != nil {
		return err
	}
	_, _ = io.WriteString(os.Stdout, token+"\n")
	return nil
}
