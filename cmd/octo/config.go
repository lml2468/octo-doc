package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// config holds the resolved client configuration: where the local doc store
// lives, which port the preview server binds, and how to reach a remote
// octo-doc server for publish/pull.
//
// Resolution order for base URL and token: environment first (OCTO_* then the
// legacy TDOC_* names), then the on-disk config file. This keeps existing tdoc
// installs working unchanged while preferring the new OCTO_* surface.
type config struct {
	BaseURL string
	Token   string
	Code    string
	Dir     string
	Port    int
}

// configFile is where publish persists {base_url, token} for later runs.
type configFile struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
}

// defaultPort is the local preview server port, preserved from tdoc.
const defaultPort = 7878

// envFirst returns the first non-empty environment variable among names.
func envFirst(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// homeDir returns the user's home directory, or "." if it can't be resolved.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// configPath returns the active config file path. It prefers ~/.octo/config.json
// but falls back to a pre-existing ~/.tdoc/config.json so legacy installs are
// read transparently. The returned bool reports whether the path already exists.
func configPath() (string, bool) {
	octo := filepath.Join(homeDir(), ".octo", "config.json")
	if _, err := os.Stat(octo); err == nil {
		return octo, true
	}
	tdoc := filepath.Join(homeDir(), ".tdoc", "config.json")
	if _, err := os.Stat(tdoc); err == nil {
		return tdoc, true
	}
	return octo, false // default write target, does not exist yet
}

// docDir resolves the local doc store: OCTO_DIR/TDOC_DIR env, else ~/octo-docs,
// falling back to an existing ~/tdocs so legacy stores keep working.
func docDir() string {
	if d := envFirst("OCTO_DIR", "TDOC_DIR"); d != "" {
		return d
	}
	octo := filepath.Join(homeDir(), "octo-docs")
	if _, err := os.Stat(octo); err == nil {
		return octo
	}
	tdoc := filepath.Join(homeDir(), "tdocs")
	if _, err := os.Stat(tdoc); err == nil {
		return tdoc
	}
	return octo // default; created on first use
}

// port resolves the preview port from OCTO_PORT/TDOC_PORT, else the default.
func port() int {
	if v := envFirst("OCTO_PORT", "TDOC_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultPort
}

// loadConfig resolves the full client config, reading the config file only to
// fill in a base URL or token not already supplied via the environment.
func loadConfig() config {
	c := config{
		BaseURL: strings.TrimRight(envFirst("OCTO_BASE_URL", "TDOC_BASE_URL"), "/"),
		Token:   envFirst("OCTO_TOKEN", "TDOC_TOKEN"),
		Code:    envFirst("OCTO_CODE"),
		Dir:     docDir(),
		Port:    port(),
	}
	if c.BaseURL == "" || c.Token == "" {
		if path, ok := configPath(); ok {
			if f, err := readConfigFile(path); err == nil {
				if c.BaseURL == "" {
					c.BaseURL = strings.TrimRight(f.BaseURL, "/")
				}
				if c.Token == "" {
					c.Token = f.Token
				}
			}
		}
	}
	return c
}

// readConfigFile parses a {base_url, token} config file.
func readConfigFile(path string) (configFile, error) {
	var f configFile
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	err = json.Unmarshal(b, &f)
	return f, err
}

// saveConfig persists {base_url, token} to ~/.octo/config.json at mode 0600
// (it holds a write token). It always writes the new OCTO path.
func saveConfig(baseURL, token string) error {
	dir := filepath.Join(homeDir(), ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	b, err := json.Marshal(configFile{BaseURL: baseURL, Token: token})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
