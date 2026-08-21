package core

import (
	"strings"
	"testing"
)

func TestSanitizeHTML(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		// --- happy path ---------------------------------------------------
		{"empty", "", ""},
		{"plain text", "hello world", "hello world"},
		{"escapes metacharacters", `a < b & c > d "q" 'p'`, `a &lt; b &amp; c &gt; d &#34;q&#34; &#39;p&#39;`},
		{"whitelisted inline", "<b>b</b><i>i</i><u>u</u>", "<b>b</b><i>i</i><u>u</u>"},
		{"uppercase tags normalised", "<B>x</B>", "<b>x</b>"},
		{"br void", "a<br>b", "a<br/>b"},
		{"br self closing", "a<br/>b", "a<br/>b"},
		{"nested inline", "<b><i>x</i></b>", "<b><i>x</i></b>"},
		{"unicode survives", "Привет 你好 🎉 <b>жирный</b>", "Привет 你好 🎉 <b>жирный</b>"},

		// --- anchors: allowed ----------------------------------------------
		{
			"https anchor",
			`<a href="https://example.com/x?y=1">l</a>`,
			`<a href="https://example.com/x?y=1" rel="noopener noreferrer">l</a>`,
		},
		{
			"http anchor",
			`<a href="http://example.com">l</a>`,
			`<a href="http://example.com" rel="noopener noreferrer">l</a>`,
		},
		{
			"anchor keeps only href",
			`<a href="http://a.io" onclick="evil()" target="_blank" style="x">l</a>`,
			`<a href="http://a.io" rel="noopener noreferrer">l</a>`,
		},

		// --- anchors: rejected schemes --------------------------------------
		{"javascript href", `<a href="javascript:alert(1)">click</a>`, "click"},
		{"javascript href mixed case", `<a href="JaVaScRiPt:alert(1)">click</a>`, "click"},
		{"javascript href entity encoded", `<a href="&#106;avascript:alert(1)">click</a>`, "click"},
		{"javascript href nul smuggled", "<a href=\"java\x00script:alert(1)\">click</a>", "click"},
		{"javascript href tab smuggled", `<a href="java&#9;script:alert(1)">click</a>`, "click"},
		{"javascript href newline smuggled", "<a href=\"java\nscript:alert(1)\">click</a>", "click"},
		{"javascript href leading space", `<a href="  javascript:alert(1)">click</a>`, "click"},
		{"data href", `<a href="data:text/html;base64,PHNjcmlwdD4=">click</a>`, "click"},
		{"vbscript href", `<a href="vbscript:msgbox(1)">click</a>`, "click"},
		{"file href", `<a href="file:///etc/passwd">click</a>`, "click"},
		{"protocol relative href", `<a href="//evil.com">click</a>`, "click"},
		{"relative href", `<a href="/foo/bar">click</a>`, "click"},
		{"fragment href", `<a href="#anchor">click</a>`, "click"},
		{"empty href", `<a href="">click</a>`, "click"},
		{"missing href", `<a>click</a>`, "click"},
		{"scheme without host", `<a href="http:">click</a>`, "click"},

		// --- scripting and opaque content ------------------------------------
		{"script dropped with body", "<script>alert(1)</script>hi", "hi"},
		{"script with attributes", `<script src="//evil.com/x.js"></script>hi`, "hi"},
		{"script unclosed at eof", "<b>a</b><script>alert(1)", "<b>a</b>"},
		{"style dropped with body", "<style>body{background:url(javascript:1)}</style>ok", "ok"},
		{"iframe dropped", `<iframe src="javascript:alert(1)"></iframe>ok`, "ok"},
		{"svg script dropped", "<svg><script>alert(1)</script></svg>ok", "ok"},
		{"textarea dropped", "<textarea><script>x</script></textarea>ok", "ok"},
		{"comment dropped", "a<!-- <script>x</script> -->b", "ab"},
		{"doctype dropped", "<!DOCTYPE html>text", "text"},

		// --- non-whitelisted markup: tag dies, text lives ---------------------
		{"img with onerror", `<img src=x onerror="alert(1)">`, ""},
		{"img inside text", `before<img src=x onerror=alert(1)>after`, "beforeafter"},
		{"div with handlers", `<div onmouseover="alert(1)">text</div>`, "text"},
		{"form and input", `<form action="http://evil"><input value="x"></form>y`, "y"},
		{"paragraphs flattened", "<p>one</p><p>two</p>", "onetwo"},
		{"mumble rich text", `<p style="margin:0"><b>x</b><br/>y</p>`, "<b>x</b><br/>y"},
		{"span with style", `<span style="font-size:99px">big</span>`, "big"},
		{"unknown tag", "<blink>x</blink>", "x"},
		{"body and html stripped", "<html><body><b>x</b></body></html>", "<b>x</b>"},

		// --- malformed input --------------------------------------------------
		{"unclosed bold", "<b>text", "<b>text</b>"},
		{"crossed tags", "<b><i>x</b></i>", "<b><i>x</i></b>"},
		{"stray end tag", "x</b>y", "xy"},
		{"end tag without open", "</i>plain", "plain"},
		{"double close", "<b>x</b></b>", "<b>x</b>"},
		{"lone lt", "a < b", "a &lt; b"},
		{"bare ampersand", "AT&T", "AT&amp;T"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeHTML(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeHTML(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeHTMLNeverLeaksExecutableMarkup is the property the whitelist
// exists for: no matter the input, the output must not contain a tag or an
// attribute a browser could execute.
func TestSanitizeHTMLNeverLeaksExecutableMarkup(t *testing.T) {
	t.Parallel()

	payloads := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<svg/onload=alert(1)>`,
		`<body onload=alert(1)>`,
		`<iframe srcdoc="<script>alert(1)</script>">`,
		`<a href="javascript:alert(1)">x</a>`,
		`<a href=javascript:alert(1)>x</a>`,
		`<b onmouseover="alert(1)">x</b>`,
		`<input autofocus onfocus=alert(1)>`,
		`<object data="javascript:alert(1)">`,
		`<embed src="javascript:alert(1)">`,
		`<math><mtext><script>alert(1)</script></mtext></math>`,
		`<noscript><p title="</noscript><img src=x onerror=alert(1)>">`,
		`<template><script>alert(1)</script></template>`,
		`<b><script>alert(1)</script></b>`,
		`<<script>alert(1)//<</script>`,
		`<a href="  JAVASCRIPT&#58;alert(1)">x</a>`,
		"<a href=\"jav\tascript:alert(1)\">x</a>",
		"<a href=\"jav&#x0A;ascript:alert(1)\">x</a>",
		`<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`,
		`<base href="javascript:">`,
		`<style>@import 'javascript:alert(1)';</style>`,
		`<b style="background:url(javascript:alert(1))">x</b>`,
	}

	banned := []string{
		"<script", "<img", "<svg", "<iframe", "<object", "<embed", "<meta",
		"<base", "<style", "<form", "<input", "<template", "<math",
		"onerror", "onload", "onfocus", "onmouseover", "javascript:", "srcdoc",
		"style=",
	}

	for _, p := range payloads {
		got := strings.ToLower(SanitizeHTML(p))
		for _, b := range banned {
			if strings.Contains(got, b) {
				t.Errorf("payload %q leaked %q: %q", p, b, got)
			}
		}
	}
}

func TestSanitizeHTMLBalancesOutput(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"<b><i><u>x",
		"<b><a href=\"http://a.io\">x",
		"<b>a<i>b</b>c</i>d",
		strings.Repeat("<b>", 50) + "x",
	}
	for _, in := range inputs {
		got := SanitizeHTML(in)
		for _, tag := range []string{"b", "i", "u", "a"} {
			opens := strings.Count(got, "<"+tag+">") + strings.Count(got, "<"+tag+" ")
			closes := strings.Count(got, "</"+tag+">")
			if opens != closes {
				t.Errorf("SanitizeHTML(%q) = %q: %d <%s> vs %d </%s>", in, got, opens, tag, closes, tag)
			}
		}
	}
}

func TestSanitizeHTMLTruncatesHugeInput(t *testing.T) {
	t.Parallel()

	in := strings.Repeat("a", maxIncomingHTML+4096)
	got := SanitizeHTML(in)
	if len(got) != maxIncomingHTML {
		t.Fatalf("len = %d, want %d", len(got), maxIncomingHTML)
	}

	// A tag cut in half by the limit must still leave balanced output.
	bombed := strings.Repeat("<b>x</b>", maxIncomingHTML)
	if out := SanitizeHTML(bombed); strings.Count(out, "<b>") != strings.Count(out, "</b>") {
		t.Fatalf("unbalanced output after truncation")
	}
}

func TestEscapePlain(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"a < b & c", "a &lt; b &amp; c"},
		{"<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"},
		{"line1\nline2", "line1<br/>line2"},
		{"crlf\r\nnext", "crlf<br/>next"},
		{`quote "q" and 'p'`, `quote &#34;q&#34; and &#39;p&#39;`},
	}
	for _, tc := range cases {
		if got := EscapePlain(tc.in); got != tc.want {
			t.Errorf("EscapePlain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Round-tripping outgoing text through the incoming sanitizer must give the
// original text back: what we send is what our own UI will render.
func TestEscapePlainSurvivesSanitize(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"plain",
		"<script>alert(1)</script>",
		"a & b < c > d",
		"emoji 🎉 and кириллица",
	} {
		if got := SanitizeHTML(EscapePlain(in)); got != EscapePlain(in) {
			t.Errorf("round trip changed %q: %q", in, got)
		}
	}
}
