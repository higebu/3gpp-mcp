package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxToolDescriptionLen is the longest tool description the OpenAI-compatible
// chat completions API accepts: "Invalid 'tools[N].function.description':
// string too long. Expected a string with maximum length 1024". Clients that
// front several model vendors with that API shape (GitHub Copilot among them)
// reject the whole tools/list when one description exceeds it, so every tool
// stays under the cap; detail that does not fit belongs in the parameter
// descriptions, which the cap does not cover.
const maxToolDescriptionLen = 1024

// TestToolDescriptionLength lists the tools through a real client session so
// the check covers exactly what a client receives from tools/list.
func TestToolDescriptionLength(t *testing.T) {
	d := setupTestDB(t)
	s := NewServer(d, NewSource(d), "test")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	for _, tool := range res.Tools {
		if n := len(tool.Description); n > maxToolDescriptionLen {
			t.Errorf("%s: description is %d characters, over the %d-character limit", tool.Name, n, maxToolDescriptionLen)
		}
		if tool.Description == "" {
			t.Errorf("%s: empty description", tool.Name)
		}
	}
}
