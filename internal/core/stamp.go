package core

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// Artifact identity (data-tdoc-aid) stamping, ported from stamp.ts.
//
// The SAME input HTML must produce the SAME stamped output byte-for-byte. All
// structural delimiters (<, >, tag names) are ASCII, so byte offsets land on the
// same logical boundaries JavaScript's UTF-16 offsets would; sliced content is
// identical bytes and therefore hashes identically via Cyrb53 (which re-encodes
// to UTF-16 internally). The one place UTF-16 semantics matter outside Cyrb53 is
// the 80-unit `head` excerpt, handled by utf16Slice.

var stampableTags = []string{
	"img", "svg", "canvas", "video", "pre", "figure", "iframe",
	"section", "aside", "blockquote", "table", "details",
}

var rawTextTags = []string{"script", "style", "textarea", "title"}

var intrinsicAttrs = []string{"viewBox", "src", "alt", "aria-label", "title"}

type stampElement struct {
	openStart    int
	openEnd      int
	closeEnd     int
	tag          string
	attrs        string
	innerHTML    string
	isVoid       bool
	cleanedAttrs string
	aid          string
}

type heading struct {
	end  int
	text string
}

// StampResult is the stamped HTML plus the artifact index.
type StampResult struct {
	HTML string
	AIDs []StampedArtifact
}

var (
	dataTdocAttrRe  = regexp.MustCompile(`\sdata-tdoc-[\w-]+\s*=\s*"[^"]*"`)
	dataTdocAidRe   = regexp.MustCompile(`\s+data-tdoc-aid\s*=\s*"[^"]*"`)
	dataTdocAidRe2  = regexp.MustCompile(`\sdata-tdoc-aid\s*=\s*"[^"]*"`)
	htmlCommentRe   = regexp.MustCompile(`(?s)<!--.*?-->`)
	whitespaceRunRe = regexp.MustCompile(`\s+`)
	tagStripRe      = regexp.MustCompile(`<[^>]+>`)
	selfCloseEndRe  = regexp.MustCompile(`/\s*$`)
	voidTagRe       = regexp.MustCompile(`(?i)^(img|iframe)$`)
	optInArtifactRe = regexp.MustCompile(`(?i)\bdata-tdoc-artifact\b`)
	optInClassRe    = regexp.MustCompile(`(?i)class\s*=\s*"[^"]*\btdoc-artifact\b[^"]*"`)
	probeTagRe      = regexp.MustCompile(`(?i)<([a-z][\w-]*)\b`)
)

// aidFor computes the content-hash aid for one artifact element.
func aidFor(tag, innerHTML, openAttrs string) string {
	var parts []string
	for _, a := range intrinsicAttrs {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(a) + `\s*=\s*"([^"]*)"`)
		if m := re.FindStringSubmatch(openAttrs); m != nil {
			parts = append(parts, a+"="+m[1])
		}
	}
	intrinsics := strings.Join(parts, "|")
	norm := htmlCommentRe.ReplaceAllString(innerHTML, "")
	norm = dataTdocAttrRe.ReplaceAllString(norm, "")
	norm = whitespaceRunRe.ReplaceAllString(norm, " ")
	norm = strings.TrimSpace(norm)
	return Cyrb53(tag+"|"+intrinsics+"|"+norm, 0)
}

// attrAwareOpenTagEnd returns the index just past the > that closes the open tag
// starting at lt, treating > inside quoted attribute values as ordinary text.
// Returns -1 if unterminated.
func attrAwareOpenTagEnd(html string, lt int) int {
	var quote byte
	for i := lt + 1; i < len(html); i++ {
		ch := html[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			quote = ch
		case '>':
			return i + 1
		}
	}
	return -1
}

// skipRawTextBodyAt returns the index just past a raw-text element's closing tag.
func skipRawTextBodyAt(html, openTag, attrs string, openEnd int) int {
	if selfCloseEndRe.MatchString(attrs) {
		return openEnd
	}
	re := regexp.MustCompile(`(?i)</` + regexp.QuoteMeta(openTag) + `\s*>`)
	loc := re.FindStringIndex(html[openEnd:])
	if loc == nil {
		return len(html)
	}
	return openEnd + loc[1]
}

// collectHeadings finds <hN> headings with their end offsets. The TS original
// uses a backreference (</h\1>) which RE2 forbids, so we loop the three heading
// levels and pair manually.
func collectHeadings(html string) []heading {
	var out []heading
	for _, level := range []string{"1", "2", "3"} {
		openRe := regexp.MustCompile(`(?i)<h` + level + `\b[^>]*>`)
		idx := 0
		for {
			loc := openRe.FindStringIndex(html[idx:])
			if loc == nil {
				break
			}
			contentStart := idx + loc[1]
			rel := indexFoldClose(html[contentStart:], "</h"+level)
			if rel < 0 {
				idx = contentStart
				continue
			}
			contentEnd := contentStart + rel
			// advance past the full close tag (</hN ...>)
			closeEndRel := strings.IndexByte(html[contentEnd:], '>')
			if closeEndRel < 0 {
				idx = contentStart
				continue
			}
			end := contentEnd + closeEndRel + 1
			inner := html[contentStart:contentEnd]
			text := tagStripRe.ReplaceAllString(inner, "")
			text = whitespaceRunRe.ReplaceAllString(text, " ")
			text = strings.TrimSpace(text)
			out = append(out, heading{end: end, text: text})
			idx = end
		}
	}
	// Sort by end offset so nearestHeading lookup (scan ascending) works as in TS,
	// where headings were collected in document order by a single regex.
	sort.SliceStable(out, func(i, j int) bool { return out[i].end < out[j].end })
	return out
}

// indexFoldClose finds the first case-insensitive occurrence of a closing tag
// prefix like "</h1" and returns the byte index of its '<', or -1.
func indexFoldClose(s, prefix string) int {
	lower := strings.ToLower(s)
	return strings.Index(lower, strings.ToLower(prefix))
}

// findCloseEnd finds the closing-tag end offset for a non-void element.
func findCloseEnd(html, tag string, openEnd int) int {
	closeRe := regexp.MustCompile(`(?i)</` + regexp.QuoteMeta(tag) + `\s*>`)
	openRe := regexp.MustCompile(`(?i)<` + regexp.QuoteMeta(tag) + `\b`)
	rawRe := regexp.MustCompile(`(?i)<(` + strings.Join(rawTextTags, "|") + `)\b`)
	depth := 1
	scan := openEnd
	for scan < len(html) {
		close := relMatch(closeRe, html, scan)
		open := relMatch(openRe, html, scan)
		raw := relMatch(rawRe, html, scan)
		next, kind := earliest(close, open, raw)
		if next == nil {
			break
		}
		switch kind {
		case "raw":
			rEnd := attrAwareOpenTagEnd(html, next[0])
			if rEnd < 0 {
				return openEnd
			}
			rawTag := strings.ToLower(html[next[2]:next[3]])
			scan = skipRawTextBodyAt(html, rawTag, html[next[0]:rEnd], rEnd)
		case "close":
			depth--
			if depth == 0 {
				return next[1]
			}
			scan = next[1]
		case "open":
			depth++
			oEnd := attrAwareOpenTagEnd(html, next[0])
			if oEnd < 0 {
				scan = next[1]
			} else {
				scan = oEnd
			}
		}
	}
	return openEnd
}

// relMatch runs re against html[from:] and returns absolute submatch indices
// ([start,end, group1start,group1end...]) or nil.
func relMatch(re *regexp.Regexp, html string, from int) []int {
	loc := re.FindStringSubmatchIndex(html[from:])
	if loc == nil {
		return nil
	}
	out := make([]int, len(loc))
	for i, v := range loc {
		if v < 0 {
			out[i] = v
		} else {
			out[i] = v + from
		}
	}
	return out
}

// earliest returns the match with the smallest start index and its kind.
func earliest(close, open, raw []int) ([]int, string) {
	var best []int
	var kind string
	consider := func(m []int, k string) {
		if m == nil {
			return
		}
		if best == nil || m[0] < best[0] {
			best, kind = m, k
		}
	}
	// Order matters only for ties; TS sorts by index with stable order
	// close,open,raw — but ties at the same index can't happen for distinct
	// patterns starting with '<' + different next char, so any order is fine.
	consider(close, "close")
	consider(open, "open")
	consider(raw, "raw")
	return best, kind
}

func harvest(html string, openStart, openEnd int, tag, attrs string, seen map[int]bool, elements *[]stampElement) {
	if seen[openStart] {
		return
	}
	isVoid := voidTagRe.MatchString(tag) || selfCloseEndRe.MatchString(attrs)
	closeEnd := openEnd
	innerHTML := ""
	if !isVoid {
		closeEnd = findCloseEnd(html, tag, openEnd)
		end := closeEnd - len("</"+tag+">")
		if end >= openEnd && end <= len(html) {
			innerHTML = html[openEnd:end]
		}
	}
	seen[openStart] = true
	*elements = append(*elements, stampElement{
		openStart: openStart, openEnd: openEnd, closeEnd: closeEnd,
		tag: tag, attrs: attrs, innerHTML: innerHTML, isVoid: isVoid,
	})
}

func harvestStampableTags(html string, seen map[int]bool, elements *[]stampElement) {
	for _, tag := range stampableTags {
		openRe := regexp.MustCompile(`(?i)<` + regexp.QuoteMeta(tag) + `\b`)
		idx := 0
		for {
			loc := openRe.FindStringIndex(html[idx:])
			if loc == nil {
				break
			}
			start := idx + loc[0]
			end := attrAwareOpenTagEnd(html, start)
			if end < 0 {
				idx = start + 1
				continue
			}
			attrs := html[start+1+len(tag) : end-1]
			harvest(html, start, end, tag, attrs, seen, elements)
			idx = start + 1
		}
	}
}

func harvestOptInMarkers(html string, seen map[int]bool, elements *[]stampElement) {
	idx := 0
	for {
		loc := probeTagRe.FindStringSubmatchIndex(html[idx:])
		if loc == nil {
			break
		}
		start := idx + loc[0]
		tag := strings.ToLower(html[idx+loc[2] : idx+loc[3]])
		end := attrAwareOpenTagEnd(html, start)
		if end < 0 {
			idx = start + 1
			continue
		}
		attrs := html[start+1+len(tag) : end-1]
		if optInArtifactRe.MatchString(attrs) || optInClassRe.MatchString(attrs) {
			harvest(html, start, end, tag, attrs, seen, elements)
		}
		idx = start + 1
	}
}

// utf16Slice returns the first n UTF-16 code units of s, matching JS slice(0,n).
func utf16Slice(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s
	}
	return string(utf16.Decode(units[:n]))
}

// StampAids stamps data-tdoc-aid on every commentable artifact in rawHTML.
func StampAids(rawHTML string) StampResult {
	headings := collectHeadings(rawHTML)
	nearestHeadingAt := func(idx int) *string {
		var best *string
		for i := range headings {
			if headings[i].end <= idx {
				t := headings[i].text
				best = &t
			} else {
				break
			}
		}
		return best
	}

	seen := map[int]bool{}
	var harvested []stampElement
	harvestStampableTags(rawHTML, seen, &harvested)
	harvestOptInMarkers(rawHTML, seen, &harvested)

	aids := []StampedArtifact{}
	elements := make([]stampElement, 0, len(harvested))
	for _, e := range harvested {
		cleanedAttrs := dataTdocAidRe.ReplaceAllString(e.attrs, "")
		cleanedInner := dataTdocAidRe2.ReplaceAllString(e.innerHTML, "")
		aid := aidFor(e.tag, cleanedInner, cleanedAttrs)
		aids = append(aids, StampedArtifact{
			AID:     aid,
			Tag:     e.tag,
			Head:    utf16Slice(e.innerHTML, 80),
			Heading: nearestHeadingAt(e.openStart),
		})
		e.cleanedAttrs = cleanedAttrs
		e.aid = aid
		elements = append(elements, e)
	}

	// Apply stamps in reverse offset order so earlier offsets stay valid.
	sort.SliceStable(elements, func(i, j int) bool {
		return elements[i].openStart > elements[j].openStart
	})
	out := rawHTML
	for _, e := range elements {
		selfClose := ""
		if selfCloseEndRe.MatchString(e.attrs) {
			selfClose = "/"
		}
		var stampedOpen string
		if e.isVoid {
			stampedOpen = "<" + e.tag + e.cleanedAttrs + ` data-tdoc-aid="` + e.aid + `"` + selfClose + ">"
		} else {
			stampedOpen = "<" + e.tag + e.cleanedAttrs + ` data-tdoc-aid="` + e.aid + `">`
		}
		out = out[:e.openStart] + stampedOpen + out[e.openEnd:]
	}
	return StampResult{HTML: out, AIDs: aids}
}
