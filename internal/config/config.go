// Package config holds the 12-factor application configuration. Every knob is an
// environment variable; the struct is parsed once at boot and treated as
// immutable thereafter. No other package reads the environment for app settings.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved, immutable application configuration.
type Config struct {
	Port    int
	Host    string
	BaseURL string
	RepoURL string

	// Storage: PostgreSQL metadata + S3-compatible blobs (the only supported stack).
	DatabaseURL      string
	PGPoolMax        int
	S3Bucket         string
	S3Region         string
	S3Endpoint       string
	S3ForcePathStyle bool
	S3AccessKeyID    string
	S3SecretKey      string

	WriteToken     string
	AllowBootstrap bool
	Private        bool
	Owner          string
	FrameAncestors string
	GitHubClientID string

	RateLimitWindow time.Duration
	RateLimitMax    int
	MaxHTMLBytes    int64
	LogLevel        string
	CookieSecure    bool

	IOTimeout time.Duration
	IORetries int
}

var slugRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Load parses and validates configuration from the process environment.
func Load() (*Config, error) {
	c := &Config{
		Port:    envInt("PORT", 8080),
		Host:    env("HOST", "0.0.0.0"),
		BaseURL: strings.TrimRight(env("BASE_URL", ""), "/"),
		RepoURL: env("REPO_URL", "https://github.com/Mininglamp-OSS/octo-doc"),

		DatabaseURL:      env("DATABASE_URL", env("PG_URL", "")),
		PGPoolMax:        envInt("PG_POOL_MAX", 10),
		S3Bucket:         env("S3_BUCKET", "octo-doc"),
		S3Region:         env("S3_REGION", env("AWS_REGION", "us-east-1")),
		S3Endpoint:       env("S3_ENDPOINT", ""),
		S3ForcePathStyle: envBool("S3_FORCE_PATH_STYLE", false),
		S3AccessKeyID:    env("S3_ACCESS_KEY_ID", env("AWS_ACCESS_KEY_ID", "")),
		S3SecretKey:      env("S3_SECRET_ACCESS_KEY", env("AWS_SECRET_ACCESS_KEY", "")),

		WriteToken:     env("WRITE_TOKEN", ""),
		AllowBootstrap: envBool("ALLOW_BOOTSTRAP", true),
		Private:        envBool("PRIVATE", false),
		Owner:          strings.TrimSpace(env("OWNER", "")),
		FrameAncestors: strings.TrimSpace(env("FRAME_ANCESTORS", "'none'")),
		GitHubClientID: env("GITHUB_CLIENT_ID", ""),

		RateLimitWindow: time.Duration(envInt("RATE_LIMIT_WINDOW_MS", 60_000)) * time.Millisecond,
		RateLimitMax:    envInt("RATE_LIMIT_MAX", 60),
		MaxHTMLBytes:    int64(envInt("MAX_HTML_BYTES", 5*1024*1024)),
		LogLevel:        env("LOG_LEVEL", "info"),
		CookieSecure:    envBool("COOKIE_SECURE", true),

		IOTimeout: time.Duration(envInt("IO_TIMEOUT_MS", 5000)) * time.Millisecond,
		IORetries: envInt("IO_RETRIES", 2),
	}
	return c, nil
}

// SafeSlug returns the slug if valid, or empty string. Single source of truth for
// slug validation.
func SafeSlug(slug string) string {
	if slugRe.MatchString(slug) {
		return slug
	}
	return ""
}

func env(key, dflt string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return dflt
}

func envInt(key string, dflt int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return dflt
}

var truthyRe = regexp.MustCompile(`^(1|true|yes|on)$`)

func envBool(key string, dflt bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		return truthyRe.MatchString(strings.ToLower(strings.TrimSpace(v)))
	}
	return dflt
}

// Validate checks that required production settings are present. Returns a
// descriptive error listing every problem.
func (c *Config) Validate() error {
	var problems []string
	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required (PostgreSQL connection string)")
	}
	if c.S3Bucket == "" {
		problems = append(problems, "S3_BUCKET is required")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}
