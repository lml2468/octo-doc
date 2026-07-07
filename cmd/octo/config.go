package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// config holds the resolved client configuration: where the local doc store
// lives and how to reach a remote octo-doc server for publish/pull.
//
// Resolution order for base URL and token: OCTO_* environment first, then the
// on-disk config file (~/.octo/config.json).
type config struct {
	BaseURL string
	Token   string
	Code    string
	Dir     string
}

// configFile is where publish persists {base_url, token} for later runs.
type configFile struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
}

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

// configPath returns the active config file path (~/.octo/config.json). The
// returned bool reports whether the path already exists.
func configPath() (string, bool) {
	octo := filepath.Join(homeDir(), ".octo", "config.json")
	if _, err := os.Stat(octo); err == nil {
		return octo, true
	}
	return octo, false // default write target, does not exist yet
}

// docDir resolves the local doc store: OCTO_DIR env, else ~/octo-docs.
func docDir() string {
	if d := os.Getenv("OCTO_DIR"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), "octo-docs") // default; created on first use
}

// loadConfig resolves the full client config, reading the config file only to
// fill in a base URL or token not already supplied via the environment.
func loadConfig() config {
	c := config{
		BaseURL: strings.TrimRight(os.Getenv("OCTO_BASE_URL"), "/"),
		Token:   os.Getenv("OCTO_TOKEN"),
		Code:    os.Getenv("OCTO_CODE"),
		Dir:     docDir(),
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
