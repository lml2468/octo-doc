package core

import "testing"

func TestEscapeHTML(t *testing.T) {
	cases := map[string]string{
		`a & b`:    `a &amp; b`,
		`<script>`: `&lt;script&gt;`,
		`"quoted"`: `&quot;quoted&quot;`,
		`it's`:     `it&#39;s`,
		`plain`:    `plain`,
		`<>&"'`:    `&lt;&gt;&amp;&quot;&#39;`,
	}
	for in, want := range cases {
		if got := EscapeHTML(in); got != want {
			t.Errorf("EscapeHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeJSONForScript(t *testing.T) {
	// Must NOT \u-escape <, >, & (match JS JSON.stringify), but MUST neutralize
	// </script> and <!--.
	cfg := OverlayConfig{Slug: "a<b>&c", Version: 1, Mode: "local", Identity: nil}
	out, err := SafeJSONForScript(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"slug":"a<b>&c"`; !contains(out, want) {
		t.Errorf("expected unescaped %q in %q", want, out)
	}

	tricky := map[string]string{"x": "</script><!--"}
	out2, err := SafeJSONForScript(tricky)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<\/script><\!--`; !contains(out2, want) {
		t.Errorf("expected neutralized %q in %q", want, out2)
	}
}

func TestInjectOverlayCfg(t *testing.T) {
	cfg := OverlayConfig{Slug: "s", Version: 2, Mode: "published", Identity: nil}
	html, err := InjectOverlayCfg("<html><body>hi</body></html>", "console.log(1)", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(html, "window.__TDOC__ = ") || !contains(html, "console.log(1)") {
		t.Errorf("overlay not injected: %s", html)
	}
	if !contains(html, "</script>\n</body></html>") {
		t.Errorf("injection point wrong: %s", html)
	}

	// No </body>: append.
	html2, _ := InjectOverlayCfg("<p>no body</p>", "x", cfg)
	if !contains(html2, "<p>no body</p><script>") {
		t.Errorf("append fallback wrong: %s", html2)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOfStr(s, sub) >= 0
}

func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
