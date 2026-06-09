package waformat

import "testing"

func TestReply(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \n\t", ""},
		{"plain text unchanged", "just a normal reply", "just a normal reply"},
		{"bold double star", "this is **important** ok", "this is *important* ok"},
		{"bold double underscore", "this is __important__ ok", "this is *important* ok"},
		{"strikethrough", "no ~~longer~~ true", "no ~longer~ true"},
		{"atx heading", "# Title", "*Title*"},
		{"atx subheading", "### Sub heading", "*Sub heading*"},
		{"heading with trailing hashes", "## Title ##", "*Title*"},
		{"dash bullet", "- first\n- second", "• first\n• second"},
		{"star bullet", "* first\n* second", "• first\n• second"},
		{"plus bullet", "+ first", "• first"},
		{"nested bullet keeps indent", "- top\n  - nested", "• top\n  • nested"},
		{"ordered list untouched", "1. first\n2. second", "1. first\n2. second"},
		{"link", "see [Anthropic](https://anthropic.com)", "see Anthropic (https://anthropic.com)"},
		{"link same text and url", "[https://x.io](https://x.io)", "https://x.io"},
		{"image", "![alt text](https://img.io/a.png)", "alt text (https://img.io/a.png)"},
		{"blockquote marker stripped", "> quoted line", "quoted line"},
		{"horizontal rule dropped", "above\n\n---\n\nbelow", "above\n\nbelow"},
		{"crlf normalized", "a\r\nb", "a\nb"},
		{"collapse blank lines", "a\n\n\n\nb", "a\n\nb"},
		{"trailing spaces trimmed", "a   \nb", "a\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Reply(tc.in); got != tc.want {
				t.Fatalf("Reply(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReplyPreservesCodeBlocks(t *testing.T) {
	t.Parallel()
	in := "Here:\n```go\nx := **notbold**\n```\ndone"
	want := "Here:\n```\nx := **notbold**\n```\ndone"
	if got := Reply(in); got != want {
		t.Fatalf("Reply(%q) = %q, want %q", in, got, want)
	}
}

func TestReplyPreservesInlineCode(t *testing.T) {
	t.Parallel()
	in := "run `make **all**` now"
	want := "run `make **all**` now"
	if got := Reply(in); got != want {
		t.Fatalf("Reply(%q) = %q, want %q", in, got, want)
	}
}

func TestReplyKeepsFenceWithoutLanguage(t *testing.T) {
	t.Parallel()
	in := "```\nplain\n```"
	if got := Reply(in); got != in {
		t.Fatalf("Reply(%q) = %q, want unchanged", in, got)
	}
}

func TestReplyIdempotent(t *testing.T) {
	t.Parallel()
	in := "# Heading\n\n- a **bold** point\n- see [docs](https://x.io)\n\n```py\ncode\n```"
	once := Reply(in)
	twice := Reply(once)
	if once != twice {
		t.Fatalf("not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}
