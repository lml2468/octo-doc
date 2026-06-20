package main

import (
	"errors"
	"log/slog"

	"github.com/Mininglamp-OSS/octo-doc/internal/config"
)

// errNotImplemented marks wiring that lands in later migration phases.
var errNotImplemented = errors.New("not implemented yet")

func serve(_ *config.Config, _ *slog.Logger) error {
	return errNotImplemented
}

func migrate(_ *config.Config, _ *slog.Logger) error {
	return errNotImplemented
}

func bootstrap(_ *config.Config) error {
	return errNotImplemented
}
