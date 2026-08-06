package web

import "strings"

// mdSegment is a run of Markdown source that is either literal code (fenced
// code block or inline code span, rendered and escaped verbatim by goldmark)
// or ordinary text.
type mdSegment struct {
	text string
	code bool
}

// splitCodeSegments splits Markdown content into text and code segments so
// pre-conversion rewrites (math protection, raw-HTML escaping) can skip the
// regions goldmark treats as literal code. Concatenating the segments in
// order reproduces content exactly.
//
// Only top-level fences and inline code spans are recognized. Fences inside
// container blocks (block quotes, list items) and indented code blocks are
// not: the DOCX converter trims every paragraph, so converted content never
// carries the leading "> " or 4-space indentation those forms require.
func splitCodeSegments(content string) []mdSegment {
	var segs []mdSegment
	var text strings.Builder   // pending chunk outside any fence
	var fenced strings.Builder // current fenced block, opening fence included
	var fenceChar byte
	fenceLen := 0

	flushText := func() {
		if text.Len() > 0 {
			segs = append(segs, splitInlineCode(text.String())...)
			text.Reset()
		}
	}

	rest := content
	for len(rest) > 0 {
		line := rest
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i+1], rest[i+1:]
		} else {
			rest = ""
		}
		if fenceChar != 0 {
			fenced.WriteString(line)
			if c, n, info := fenceInfo(line); c == fenceChar && n >= fenceLen && strings.TrimSpace(info) == "" {
				segs = append(segs, mdSegment{fenced.String(), true})
				fenced.Reset()
				fenceChar = 0
			}
			continue
		}
		// A backtick fence's info string may not contain a backtick
		// (CommonMark 4.5); such a line is not a fence opener.
		if c, n, info := fenceInfo(line); c != 0 && (c != '`' || !strings.Contains(info, "`")) {
			flushText()
			fenceChar, fenceLen = c, n
			fenced.WriteString(line)
			continue
		}
		text.WriteString(line)
	}
	flushText()
	if fenced.Len() > 0 { // unterminated fence runs to end of input
		segs = append(segs, mdSegment{fenced.String(), true})
	}
	return segs
}

// fenceInfo reports whether line is a code fence: it returns the fence
// character ('`' or '~', 0 if not a fence), the run length, and the text
// after the run (the info string on an opener, which must be blank on a
// closer).
func fenceInfo(line string) (char byte, length int, info string) {
	s := strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimLeft(s, " ")
	if len(s)-len(trimmed) > 3 { // 4+ spaces of indent is an indented code line
		return 0, 0, ""
	}
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0, ""
	}
	c := trimmed[0]
	n := 0
	for n < len(trimmed) && trimmed[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, ""
	}
	return c, n, trimmed[n:]
}

// splitInlineCode splits a chunk containing no fenced blocks into text and
// inline-code segments. Per CommonMark 6.1, a code span opens with a run of
// backticks and closes at the next run of exactly the same length; an
// unmatched run is literal text, and a span may not cross a blank line.
func splitInlineCode(chunk string) []mdSegment {
	var segs []mdSegment
	textStart := 0
	i := 0
	for i < len(chunk) {
		if chunk[i] != '`' {
			i++
			continue
		}
		n := backtickRun(chunk, i)
		// A run preceded by an odd number of backslashes is an escaped
		// literal backtick (CommonMark 6.1) and cannot open a span.
		if escaped(chunk, i) {
			i += n
			continue
		}
		end := findBacktickCloser(chunk, i+n, n)
		if end < 0 || containsBlankLine(chunk[i:end]) {
			i += n
			continue
		}
		if i > textStart {
			segs = append(segs, mdSegment{chunk[textStart:i], false})
		}
		segs = append(segs, mdSegment{chunk[i : end+n], true})
		i = end + n
		textStart = i
	}
	if textStart < len(chunk) {
		segs = append(segs, mdSegment{chunk[textStart:], false})
	}
	return segs
}

// escaped reports whether the byte at i is preceded by an odd number of
// backslashes.
func escaped(s string, i int) bool {
	n := 0
	for i-n-1 >= 0 && s[i-n-1] == '\\' {
		n++
	}
	return n%2 == 1
}

// containsBlankLine reports whether s contains a blank line: a line holding
// nothing but spaces or tabs (CommonMark 2.1), not just a literal "\n\n".
// Only a line terminated by a newline counts — callers pass the range up to
// the closing backtick run, whose own line always carries the closer and can
// therefore never be blank.
func containsBlankLine(s string) bool {
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return false
		}
		rest := s[i+1:]
		j := 0
		for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t') {
			j++
		}
		if j < len(rest) && rest[j] == '\n' {
			return true
		}
		s = rest
	}
}

// backtickRun returns the length of the backtick run starting at i.
func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// findBacktickCloser returns the index of the next run of exactly n backticks
// at or after from, or -1 if none exists.
func findBacktickCloser(s string, from, n int) int {
	for i := from; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := backtickRun(s, i)
		if run == n {
			return i
		}
		i += run
	}
	return -1
}
