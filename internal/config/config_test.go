package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Port)
	}
	if cfg.MaxHTMLBytes != 5*1024*1024 {
		t.Errorf("default max bytes = %d", cfg.MaxHTMLBytes)
	}
	if !cfg.AllowBootstrap {
		t.Error("bootstrap should default on")
	}
	if cfg.FrameAncestors != "'none'" {
		t.Errorf("frame ancestors = %q", cfg.FrameAncestors)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("PORT", "9999")
	t.Setenv("RATE_LIMIT_MAX", "0")
	t.Setenv("BASE_URL", "https://x.example.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9999 {
		t.Errorf("port = %d", cfg.Port)
	}
	if cfg.RateLimitMax != 0 {
		t.Errorf("rate max = %d", cfg.RateLimitMax)
	}
	if cfg.BaseURL != "https://x.example.com" {
		t.Errorf("base url trailing slash not trimmed: %q", cfg.BaseURL)
	}
}

func TestValidate(t *testing.T) {
	// A minimally-valid config: required strings present + positive numeric knobs.
	valid := func() *Config {
		return &Config{
			DatabaseURL: "x", S3Bucket: "b",
			Port: 8080, PGPoolMax: 10, RateLimitMax: 60, MaxHTMLBytes: 1 << 20,
			MaxAssetBytes: 1 << 20, AssetMIMEAllow: []string{"image/png"},
		}
	}
	if err := valid().Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}

	c := valid()
	c.DatabaseURL = ""
	if err := c.Validate(); err == nil {
		t.Error("missing DATABASE_URL should fail validation")
	}
	c = valid()
	c.S3Bucket = ""
	if err := c.Validate(); err == nil {
		t.Error("missing S3_BUCKET should fail validation")
	}
	c = valid()
	c.Port = 0
	if err := c.Validate(); err == nil {
		t.Error("zero PORT should fail validation")
	}
	c = valid()
	c.MaxHTMLBytes = 0
	if err := c.Validate(); err == nil {
		t.Error("zero MAX_HTML_BYTES should fail validation")
	}
	c = valid()
	c.S3Endpoint = "http://minio:9000" // custom endpoint without creds
	if err := c.Validate(); err == nil {
		t.Error("S3_ENDPOINT without credentials should fail validation")
	}
}

func TestSafeSlug(t *testing.T) {
	valid := []string{"hello", "a_b-c", "ABC123", "x"}
	for _, s := range valid {
		if SafeSlug(s) != s {
			t.Errorf("SafeSlug(%q) rejected a valid slug", s)
		}
	}
	invalid := []string{"", "../etc", "a/b", "a b", "a.b", strings95()}
	for _, s := range invalid {
		if SafeSlug(s) != "" {
			t.Errorf("SafeSlug(%q) accepted an invalid slug", s)
		}
	}
}

func strings95() string {
	b := make([]byte, 95)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
