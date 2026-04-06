package tools

import (
	"fmt"
	"strings"

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
		}
	}

	// Smart cut: extend to the next paragraph boundary (empty line).
	// maxLines * 1.2 caps how far we look ahead.
	if end < totalLines {
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
