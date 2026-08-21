package core

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Mumble text messages are HTML authored by other clients, i.e. fully
// untrusted input that ends up in the webview. SanitizeHTML rewrites it into a
// tiny whitelist (PLAN.md §5): b, i, u, br and http(s) anchors. Everything else
// keeps its text content (escaped) and loses its markup.
//
// The parse step uses golang.org/x/net/html's tokenizer so that the input is
// split exactly the way a browser would split it; the whitelist itself is ours,
// so no attribute other than a well-formed href ever reaches the output.

// maxIncomingHTML bounds the work a single hostile message can cause. Mumble
// servers cap message length far below this (default 5000 characters), so the
// limit only ever fires on a misbehaving peer. Truncating mid-tag is safe: the
// tokenizer stops at EOF and the writer closes whatever is still open.
const maxIncomingHTML = 64 << 10

// inlineTags survive with their markup. br is handled separately (void element).
var inlineTags = map[atom.Atom]string{
	atom.B: "b",
	atom.I: "i",
	atom.U: "u",
}

// opaqueTags carry script-ish or non-HTML payloads. Their markup AND their text
// content are dropped: escaping `alert(1)` into the chat would be safe but
// nonsensical, and foreign-content elements (svg, math) can re-enable scripting
// constructs a plain escape would not neutralise.
var opaqueTags = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Title:    true,
	atom.Textarea: true,
	atom.Iframe:   true,
	atom.Noscript: true,
	atom.Noframes: true,
	atom.Noembed:  true,
	atom.Object:   true,
	atom.Embed:    true,
	atom.Template: true,
	atom.Xmp:      true,
	atom.Svg:      true,
	atom.Math:     true,
}

// SanitizeHTML returns markup safe to hand to the UI as trusted HTML.
func SanitizeHTML(in string) string {
	if len(in) > maxIncomingHTML {
		in = in[:maxIncomingHTML]
	}

	var (
		out      strings.Builder
		open     []string // whitelisted elements we have emitted and not closed
		opaque   []string // nesting of dropped-content elements
		tokenizr = html.NewTokenizer(strings.NewReader(in))
	)
	out.Grow(len(in))

	for {
		switch tokenizr.Next() {
		case html.ErrorToken:
			// EOF or malformed input: close what we opened so a stray "<b>"
			// cannot bleed formatting into the rest of the chat transcript.
			for i := len(open) - 1; i >= 0; i-- {
				out.WriteString("</")
				out.WriteString(open[i])
				out.WriteString(">")
			}
			return out.String()

		case html.TextToken:
			if len(opaque) == 0 {
				out.WriteString(html.EscapeString(string(tokenizr.Text())))
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokenizr.TagName()
			a := atom.Lookup(name)
			if opaqueTags[a] {
				// Pushed even when self-closing: the tokenizer switches to raw
				// text after "<script/>" just as it does after "<script>", so
				// the suppression window has to match its behaviour.
				opaque = append(opaque, string(name))
				continue
			}
			if len(opaque) > 0 {
				continue
			}
			switch {
			case a == atom.Br:
				out.WriteString("<br/>")
			case inlineTags[a] != "":
				tag := inlineTags[a]
				out.WriteString("<")
				out.WriteString(tag)
				out.WriteString(">")
				open = append(open, tag)
			case a == atom.A:
				href, ok := safeHref(tokenizr, hasAttr)
				if !ok {
					// Drop the anchor, keep its text: a javascript: link must
					// not silently become a clickable element.
					continue
				}
				out.WriteString(`<a href="`)
				out.WriteString(html.EscapeString(href))
				out.WriteString(`" rel="noopener noreferrer">`)
				open = append(open, "a")
			}
			// Any other tag: markup dropped, text content kept.

		case html.EndTagToken:
			name, _ := tokenizr.TagName()
			if n := len(opaque); n > 0 {
				if opaque[n-1] == string(name) {
					opaque = opaque[:n-1]
				}
				continue
			}
			tag := whitelistedEnd(atom.Lookup(name))
			if tag == "" {
				continue
			}
			// Close only tags we actually emitted, innermost first, so the
			// output stays balanced no matter how crossed the input was.
			for i := len(open) - 1; i >= 0; i-- {
				if open[i] != tag {
					continue
				}
				for j := len(open) - 1; j >= i; j-- {
					out.WriteString("</")
					out.WriteString(open[j])
					out.WriteString(">")
				}
				open = open[:i]
				break
			}

		default:
			// Comments, doctypes: dropped entirely.
		}
	}
}

func whitelistedEnd(a atom.Atom) string {
	if a == atom.A {
		return "a"
	}
	return inlineTags[a]
}

// safeHref extracts the single attribute we keep. Only absolute http/https URLs
// pass; relative, protocol-relative and every exotic scheme are rejected.
func safeHref(z *html.Tokenizer, hasAttr bool) (string, bool) {
	for hasAttr {
		var key, val []byte
		key, val, hasAttr = z.TagAttr()
		if string(key) != "href" {
			continue
		}
		raw := stripURLNoise(string(val))
		if raw == "" {
			return "", false
		}
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		// url.Parse lowercases the scheme, so "JaVaScRiPt:" is caught here.
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", false
		}
		if u.Host == "" {
			return "", false
		}
		return u.String(), true
	}
	return "", false
}

// stripURLNoise removes the characters browsers ignore while resolving a URL
// (ASCII control characters, including the NUL and tabs used to smuggle
// "java\x00script:"), then trims surrounding whitespace.
func stripURLNoise(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r <= 0x1f || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// EscapePlain prepares outgoing user text for Mumble, which transports chat as
// HTML. Newlines become <br/>; everything else is escaped.
func EscapePlain(text string) string {
	escaped := html.EscapeString(strings.ReplaceAll(text, "\r\n", "\n"))
	return strings.ReplaceAll(escaped, "\n", "<br/>")
}
