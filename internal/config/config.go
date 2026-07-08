// Package config holds the 12-factor application configuration. Every knob is an
// environment variable; the struct is parsed once at boot and treated as
// immutable thereafter. No other package reads the environment for app settings.
package config

import (
	"fmt"
	"math"
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
	Owner          string
	FrameAncestors string
	// TrustProxyHeaders enables honoring X-Forwarded-For / X-Real-IP for the client
	// IP (rate limiting). Enable ONLY when the server sits behind a trusted reverse
	// proxy that sets these; otherwise a client can spoof them to evade limits.
	TrustProxyHeaders bool
	// CORSOrigins is the allowlist of origins permitted on mutating /v1 routes. Empty
	// means no Access-Control-Allow-Origin is sent on writes (same-origin only).
	CORSOrigins []string

	RateLimitWindow time.Duration
	RateLimitMax    int
	MaxHTMLBytes    int64
	// MaxAssetBytes caps a single uploaded media asset. Assets are stored whole, so
	// this bounds per-request memory and object size. See docs/ASSETS.md.
	MaxAssetBytes int64
	// AssetMIMEAllow is the allowlist of MIME types accepted for asset uploads. The
	// server sniffs the bytes and rejects anything not in this set.
	AssetMIMEAllow []string
	LogLevel       string
	CookieSecure   bool

	IOTimeout time.Duration
	IORetries int
}

var slugRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// defaultAssetMIMEAllow is the conservative default set of MIME types accepted for
// media asset uploads: common images, audio/video, and PDF. See docs/ASSETS.md.
const defaultAssetMIMEAllow = "image/png,image/jpeg,image/gif,image/webp,image/avif,image/svg+xml," +
	"video/mp4,video/webm,audio/mpeg,audio/ogg,audio/wav,application/pdf"

// Load parses and validates configuration from the process environment.
func Load() (*Config, error) {
	c := &Config{
		Port:    envInt("PORT", 8080),
		Host:    env("HOST", "0.0.0.0"),
		BaseURL: strings.TrimRight(env("BASE_URL", ""), "/"),
		RepoURL: env("REPO_URL", "https://github.com/lml2468/octo-doc"),

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
		Owner:          strings.TrimSpace(env("OWNER", "")),
		FrameAncestors: strings.TrimSpace(env("FRAME_ANCESTORS", "'none'")),

		TrustProxyHeaders: envBool("TRUST_PROXY_HEADERS", false),
		CORSOrigins:       splitList(env("CORS_ORIGINS", "")),

		RateLimitWindow: time.Duration(envInt("RATE_LIMIT_WINDOW_MS", 60_000)) * time.Millisecond,
		RateLimitMax:    envInt("RATE_LIMIT_MAX", 60),
		MaxHTMLBytes:    int64(envInt("MAX_HTML_BYTES", 5*1024*1024)),
		MaxAssetBytes:   int64(envInt("MAX_ASSET_BYTES", 25*1024*1024)),
		AssetMIMEAllow:  splitList(env("ASSET_MIME_ALLOW", defaultAssetMIMEAllow)),
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

// splitList parses a comma-separated env value into a trimmed, non-empty slice.
// An empty or all-whitespace input yields a nil slice (the loop skips empties).
func splitList(v string) []string {
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
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
	if c.Port <= 0 || c.Port > 65535 {
		problems = append(problems, fmt.Sprintf("PORT must be 1..65535, got %d", c.Port))
	}
	if c.PGPoolMax <= 0 || c.PGPoolMax > math.MaxInt32 {
		problems = append(problems, fmt.Sprintf("PG_POOL_MAX must be 1..%d, got %d", math.MaxInt32, c.PGPoolMax))
	}
	if c.RateLimitMax < 0 {
		problems = append(problems, fmt.Sprintf("RATE_LIMIT_MAX must be >= 0, got %d", c.RateLimitMax))
	}
	if c.MaxHTMLBytes <= 0 {
		problems = append(problems, fmt.Sprintf("MAX_HTML_BYTES must be positive, got %d", c.MaxHTMLBytes))
	}
	if c.MaxAssetBytes <= 0 {
		problems = append(problems, fmt.Sprintf("MAX_ASSET_BYTES must be positive, got %d", c.MaxAssetBytes))
	}
	if len(c.AssetMIMEAllow) == 0 {
		problems = append(problems, "ASSET_MIME_ALLOW must list at least one MIME type")
	}
	// A custom S3 endpoint (MinIO/R2) has no ambient credential chain, so static
	// creds are required; on AWS the default chain may supply them, so only warn by
	// requiring them when an endpoint is set.
	if c.S3Endpoint != "" && (c.S3AccessKeyID == "" || c.S3SecretKey == "") {
		problems = append(problems, "S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required when S3_ENDPOINT is set")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}
