package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/assets"
	"github.com/Mininglamp-OSS/octo-doc/internal/core"
)

// cmdPreview runs the local preview server subcommands:
//
//	octo preview [serve]   run in the foreground (default)
//	octo preview start     start in the background (detached), print the URL
//	octo preview stop      stop a background preview server
//	octo preview status    report whether the server is up and is ours
func cmdPreview(args []string) error {
	sub := "serve"
	if len(args) > 0 {
		sub = args[0]
	}
	cfg := loadConfig()
	switch sub {
	case "serve":
		return previewServe(cfg)
	case "start":
		return previewStart(cfg)
	case "stop":
		return previewStop(cfg)
	case "status":
		return previewStatus(cfg)
	default:
		return fmt.Errorf("usage: octo preview [serve|start|stop|status]")
	}
}

// previewServer holds the running preview server's dependencies.
type previewServer struct {
	store *store
	port  int
	cfg   config // for the in-process /v1/publish handler
}

// pidPath is where a backgrounded preview server records its PID (under the doc dir).
func (c config) pidPath() string { return filepath.Join(c.Dir, ".preview.pid") }

// logPath is the background server's log file.
func (c config) logPath() string { return filepath.Join(c.Dir, ".server.log") }

// previewServe runs the HTTP server in the foreground, bound to loopback. The
// local server has no auth by design, so binding all interfaces would expose the
// unauthenticated comment/publish API to the LAN.
func previewServe(cfg config) error {
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return err
	}
	ps := &previewServer{store: newStore(cfg.Dir), port: cfg.Port, cfg: cfg}
	srv := &http.Server{
		Handler:           ps.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(cfg.Port))
	if err != nil {
		return fmt.Errorf("bind :%d: %w", cfg.Port, err)
	}
	fmt.Printf("octo preview: http://localhost:%d  (root: %s)\n", cfg.Port, cfg.Dir)
	fmt.Println("mode: local (anonymous, no auth) — bound to 127.0.0.1")
	return srv.Serve(ln)
}

// previewStart launches `octo preview serve` detached, waits until it answers as
// ours, and records the PID. Idempotent: if a healthy octo preview already runs,
// it returns without starting a second one.
func previewStart(cfg config) error {
	if pingIsOurs(cfg.Port) {
		fmt.Printf("octo preview already up at http://localhost:%d\n", cfg.Port)
		return nil
	}
	if portAnswers(cfg.Port) {
		return fmt.Errorf("port %d is answering HTTP but is not the octo preview server (foreign service); free it or set OCTO_PORT", cfg.Port)
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(cfg.logPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.Command(self, "preview", "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(cfg.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	// Release the child so it keeps running after this process exits.
	_ = cmd.Process.Release()
	for range 20 {
		time.Sleep(150 * time.Millisecond)
		if pingIsOurs(cfg.Port) {
			fmt.Printf("octo preview: http://localhost:%d\n", cfg.Port)
			return nil
		}
	}
	return fmt.Errorf("preview server did not come up; see %s", cfg.logPath())
}

// previewStop stops a backgrounded preview server via its recorded PID.
func previewStop(cfg config) error {
	b, err := os.ReadFile(cfg.pidPath())
	if err != nil {
		return errors.New("no preview server PID recorded (not started by `octo preview start`?)")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("bad pid file: %w", err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		// Fall back to Kill if the process is already gone or ignores SIGINT.
		_ = proc.Kill()
	}
	_ = os.Remove(cfg.pidPath())
	fmt.Println("octo preview stopped.")
	return nil
}

// previewStatus reports whether the preview server is up and is ours.
func previewStatus(cfg config) error {
	switch {
	case pingIsOurs(cfg.Port):
		fmt.Printf("up: http://localhost:%d (octo)\n", cfg.Port)
	case portAnswers(cfg.Port):
		fmt.Printf("port %d answers HTTP but is not octo (foreign service)\n", cfg.Port)
	default:
		fmt.Printf("down (nothing on :%d)\n", cfg.Port)
	}
	return nil
}

// renderDoc reads a version's HTML and injects the canonical overlay + boot
// config via core.InjectOverlayCfg — the exact function and overlay bytes the
// server uses, so a local preview renders identically to the published doc.
func (ps *previewServer) renderDoc(slug string, v int) (string, error) {
	raw, err := os.ReadFile(ps.store.htmlPath(slug, v))
	if err != nil {
		return "", err
	}
	// Hand the overlay the full version list for its version picker; fall back to
	// the current version only if meta.json is missing/unreadable.
	versions := []core.VersionRef{{N: v}}
	if meta, err := ps.store.readMeta(slug); err == nil && len(meta.Versions) > 0 {
		versions = versions[:0]
		for _, vr := range meta.sortedVersions() {
			created := vr.Created
			var cp *string
			if created != "" {
				cp = &created
			}
			versions = append(versions, core.VersionRef{N: vr.N, Created: cp})
		}
	}
	cfg := core.OverlayConfig{
		Slug:           slug,
		Version:        v,
		Identity:       nil,
		Mode:           "local",
		AuthConfigured: false,
		Versions:       versions,
	}
	return core.InjectOverlayCfg(string(raw), assets.OverlayJS, cfg)
}
