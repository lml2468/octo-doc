package core

import "testing"

// StampAids is a byte-exact port; these tests pin its observable behavior — which
// tags get a data-odoc-aid, the exact aid strings (a function of the frozen
// Cyrb53 over stripped content), idempotence, and the parse traps (attribute
// values containing '>', raw-text tags, void elements, already-stamped input).

func TestStampStampsArtifactTags(t *testing.T) {
	in := `<body><section><p>hi</p><img src="a.png"></section></body>`
	want := `<body><section data-odoc-aid="1l6mnuqtjhy"><p>hi</p><img src="a.png" data-odoc-aid="1etotygyt3m"></section></body>`
	res := StampAids(in)
	if res.HTML != want {
		t.Errorf("HTML:\n got %q\nwant %q", res.HTML, want)
	}
	if len(res.AIDs) != 2 {
		t.Fatalf("aids = %d, want 2", len(res.AIDs))
	}
	tags := map[string]bool{res.AIDs[0].Tag: true, res.AIDs[1].Tag: true}
	if !tags["section"] || !tags["img"] {
		t.Errorf("tags = %q/%q, want the set {section, img}", res.AIDs[0].Tag, res.AIDs[1].Tag)
	}
}

func TestStampNoArtifactsIsPassthrough(t *testing.T) {
	in := `<body><p>plain text no artifacts</p></body>`
	res := StampAids(in)
	if res.HTML != in {
		t.Errorf("passthrough changed HTML: %q", res.HTML)
	}
	if len(res.AIDs) != 0 {
		t.Errorf("aids = %d, want 0", len(res.AIDs))
	}
}

func TestStampIsIdempotent(t *testing.T) {
	in := `<body><section><p>x</p></section></body>`
	once := StampAids(in)
	twice := StampAids(once.HTML)
	if once.HTML != twice.HTML {
		t.Errorf("not idempotent:\n once %q\ntwice %q", once.HTML, twice.HTML)
	}
	// A pre-stamped element keeps its existing aid rather than getting a new one.
	pre := `<body><section data-odoc-aid="14m9wlpaboz"><p>x</p></section></body>`
	if got := StampAids(pre); got.HTML != pre {
		t.Errorf("re-stamped an already-stamped doc: %q", got.HTML)
	}
}

// The tag scanner must not treat a '>' inside a quoted attribute value as the end
// of the tag — a classic HTML-parse trap.
func TestStampAttributeValueWithGreaterThan(t *testing.T) {
	in := `<body><img alt="a > b" src="x.png"></body>`
	want := `<body><img alt="a > b" src="x.png" data-odoc-aid="2b8fykuv7qz"></body>`
	if got := StampAids(in); got.HTML != want {
		t.Errorf("attr-with-> :\n got %q\nwant %q", got.HTML, want)
	}
}

// Raw-text tags (script/style) are never stamped, and content inside them must not
// be mis-scanned for artifact tags.
func TestStampSkipsRawTextTags(t *testing.T) {
	in := `<body><script>var x=1</script><section><p>y</p></section></body>`
	res := StampAids(in)
	if len(res.AIDs) != 1 || res.AIDs[0].Tag != "section" {
		t.Fatalf("aids = %+v, want a single section", res.AIDs)
	}
	want := `<body><script>var x=1</script><section data-odoc-aid="1ywg46qkab5"><p>y</p></section></body>`
	if res.HTML != want {
		t.Errorf("script-skip:\n got %q\nwant %q", res.HTML, want)
	}
}

func TestStampVoidAndSvg(t *testing.T) {
	// Void element (img) gets the attribute inside the self-terminating tag.
	if got := StampAids(`<body><img src="a.png"></body>`).HTML; got != `<body><img src="a.png" data-odoc-aid="1etotygyt3m"></body>` {
		t.Errorf("void img: %q", got)
	}
	// SVG is stampable; viewBox is preserved verbatim (case-sensitive attr).
	svg := `<body><svg viewBox="0 0 24 24"><path d="M3 8"/></svg></body>`
	want := `<body><svg viewBox="0 0 24 24" data-odoc-aid="28osv6m0k8m"><path d="M3 8"/></svg></body>`
	if got := StampAids(svg).HTML; got != want {
		t.Errorf("svg:\n got %q\nwant %q", got, want)
	}
}

// The same content stamped twice yields the same aid (content-addressed); changing
// the content changes the aid.
func TestStampAidIsContentAddressed(t *testing.T) {
	a := StampAids(`<body><section><p>alpha</p></section></body>`).AIDs
	b := StampAids(`<body><section><p>alpha</p></section></body>`).AIDs
	c := StampAids(`<body><section><p>beta</p></section></body>`).AIDs
	if len(a) != 1 || len(b) != 1 || len(c) != 1 {
		t.Fatalf("expected one aid each: %d/%d/%d", len(a), len(b), len(c))
	}
	if a[0].AID != b[0].AID {
		t.Errorf("same content gave different aids: %s vs %s", a[0].AID, b[0].AID)
	}
	if a[0].AID == c[0].AID {
		t.Errorf("different content gave the same aid: %s", a[0].AID)
	}
}
