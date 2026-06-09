// Package waformat converts the Markdown that coding agents (Codex, Claude,
// etc.) typically emit into WhatsApp-friendly text.
//
// WhatsApp understands a small, non-Markdown set of formatting markers:
//
//	*bold*      _italic_      ~strikethrough~      ```monospace block```
//
// It does NOT understand Markdown headings, ** bold, __ bold, ~~ strike,
// [text](links), bullet dashes, or horizontal rules, so agent replies render
// with stray asterisks, hashes, and link syntax. Reply rewrites the common
// constructs into the WhatsApp equivalents.
//
// The conversion is deliberately conservative: fenced code blocks and inline
// code spans are left untouched (only the info-string/language tag on a fence
// is dropped), real newlines are preserved, and plain text passes through
// unchanged. It is not a full Markdown parser; it targets the handful of
// patterns that actually look broken in WhatsApp.
package waformat

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reFence        = regexp.MustCompile("(?s)```.*?```")
	reInlineCode   = regexp.MustCompile("`[^`\n]+`")
	reBoldStars    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBoldUnders   = regexp.MustCompile(`__(.+?)__`)
	reStrike       = regexp.MustCompile(`~~(.+?)~~`)
	reImage        = regexp.MustCompile(`!\[([^\]]*)\]\(\s*([^)\s]+)(?:\s+"[^"]*")?\s*\)`)
	reLink         = regexp.MustCompile(`\[([^\]]+)\]\(\s*([^)\s]+)(?:\s+"[^"]*")?\s*\)`)
	reHeading      = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	reBullet       = regexp.MustCompile(`^(\s*)[-*+][ \t]+`)
	reBlockquote   = regexp.MustCompile(`^\s{0,3}>[ \t]?`)
	reTrailingWS   = regexp.MustCompile(`[ \t]+\n`)
	reManyNewlines = regexp.MustCompile(`\n{3,}`)
)

// Reply rewrites agent Markdown into WhatsApp-friendly text.
func Reply(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	// NUL is used as a placeholder sentinel below; strip any that snuck in.
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	// Protect fenced code blocks, then inline code spans, so none of the
	// formatting rewrites run inside code.
	var protected []string
	protect := func(re *regexp.Regexp, transform func(string) string) {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			placeholder := fmt.Sprintf("\x00%d\x00", len(protected))
			protected = append(protected, transform(match))
			return placeholder
		})
	}
	protect(reFence, stripFenceInfoString)
	protect(reInlineCode, func(m string) string { return m })

	// Block-level rewrites, line by line.
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		switch {
		case isHorizontalRule(line):
			lines[i] = ""
		case reHeading.MatchString(line):
			if m := reHeading.FindStringSubmatch(line); strings.TrimSpace(m[1]) != "" {
				lines[i] = "*" + strings.TrimSpace(m[1]) + "*"
			}
		default:
			line = reBlockquote.ReplaceAllString(line, "")
			line = reBullet.ReplaceAllString(line, "$1• ")
			lines[i] = line
		}
	}
	s = strings.Join(lines, "\n")

	// Inline rewrites (code is placeheld, so this is safe).
	s = reImage.ReplaceAllString(s, "$1 ($2)")
	s = reLink.ReplaceAllStringFunc(s, func(match string) string {
		m := reLink.FindStringSubmatch(match)
		text, url := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if text == "" || text == url {
			return url
		}
		return text + " (" + url + ")"
	})
	s = reBoldStars.ReplaceAllString(s, "*$1*")
	s = reBoldUnders.ReplaceAllString(s, "*$1*")
	s = reStrike.ReplaceAllString(s, "~$1~")

	// Restore protected spans (reverse order keeps nested indices valid).
	for i := len(protected) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00%d\x00", i), protected[i])
	}

	// Tidy whitespace.
	s = reTrailingWS.ReplaceAllString(s, "\n")
	s = reManyNewlines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// isHorizontalRule reports whether a line is a Markdown thematic break
// (three or more of -, *, or _, optionally spaced). RE2 has no backreferences,
// so this is checked directly rather than with a regexp.
func isHorizontalRule(line string) bool {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return false
	}
	var marker byte
	count := 0
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c != '-' && c != '*' && c != '_' {
			return false
		}
		if marker == 0 {
			marker = c
		} else if c != marker {
			return false
		}
		count++
	}
	return count >= 3
}

// stripFenceInfoString removes the language/info tag from a fenced code block's
// opening line (```go -> ```), which otherwise shows up as a stray word in
// WhatsApp. The fences themselves are kept because WhatsApp renders ``` as a
// monospace block.
func stripFenceInfoString(block string) string {
	nl := strings.IndexByte(block, '\n')
	if nl < 0 {
		return block
	}
	first := strings.TrimRight(strings.TrimPrefix(block[:nl], "```"), " \t")
	if first != "" && !strings.ContainsAny(first, " \t") {
		return "```" + block[nl:]
	}
	return block
}
