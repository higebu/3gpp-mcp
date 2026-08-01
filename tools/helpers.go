package tools

import (
	"errors"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/specver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultMaxLines = 200

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

// versionErrorResult renders an error from version resolution. A fetch that is
// still running is not a failure — the caller just has to come back — so it is
// reported as ordinary text rather than as a tool error.
func versionErrorResult(err error, prefix string) *mcp.CallToolResult {
	var inProgress *FetchInProgressError
	if errors.As(err, &inProgress) {
		return textResult(inProgress.Error())
	}
	var unavailable *VersionUnavailableError
	if errors.As(err, &unavailable) {
		return errorResult(unavailable.Error())
	}
	return errorResult(fmt.Sprintf("%s: %v", prefix, err))
}

// prependLine prefixes the text content of a single-TextContent result with a
// header line, outside of pagination: line offsets and the [Lines a-b of N]
// markers are unaffected, so the header survives on every page.
func prependLine(header string, res *mcp.CallToolResult) *mcp.CallToolResult {
	if header == "" || len(res.Content) == 0 {
		return res
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		tc.Text = header + "\n" + tc.Text
	}
	return res
}

// specLabel formats a section's spec ID with its version/release,
// e.g. "TS 23.501 v18.6.0 (Rel-18)". Empty fields are omitted.
func specLabel(s db.Section) string {
	label := s.SpecID
	if s.Version != "" {
		label += " v" + s.Version
	}
	if rel := specver.ReleaseLabel(s.Release); rel != "" {
		label += " (" + rel + ")"
	}
	return label
}

func paginateText(content string, offset, maxLines, maxChars int) *mcp.CallToolResult {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if offset < 0 {
		offset = 0
	}
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}

	if offset >= totalLines {
		return textResult(fmt.Sprintf("[No content at offset %d. Total lines: %d]", offset, totalLines))
	}

	end := offset + maxLines
	if end > totalLines {
		end = totalLines
	}

	charLimited := false
	if maxChars > 0 {
		charCount := 0
		charEnd := end
		for i := offset; i < end; i++ {
			charCount += len(lines[i]) + 1
			if charCount > maxChars {
				if i > offset {
					charEnd = i
				} else {
					charEnd = i + 1
				}
				break
			}
		}
		if charEnd < end {
			end = charEnd
			charLimited = true
		}
	}

	// Smart cut: extend to the next paragraph boundary (empty line).
	// maxLines * 1.2 caps how far we look ahead. When the character budget
	// decided the cut, extending would silently exceed it, so skip.
	if !charLimited && end < totalLines {
		linesUsed := end - offset
		hardLimit := end + linesUsed/5
		if hardLimit <= end {
			hardLimit = end + 1
		}
		if hardLimit > totalLines {
			hardLimit = totalLines
		}
		for i := end; i < hardLimit; i++ {
			if lines[i] == "" {
				end = i + 1
				break
			}
		}
	}

	truncated := end < totalLines

	var sb strings.Builder
	fmt.Fprintf(&sb, "[Lines %d-%d of %d]\n\n", offset+1, end, totalLines)
	for i := offset; i < end; i++ {
		if i > offset {
			sb.WriteByte('\n')
		}
		sb.WriteString(lines[i])
	}
	if truncated {
		fmt.Fprintf(&sb, "\n\n[Truncated. Use offset=%d to continue]", end)
	}

	return textResult(sb.String())
}
