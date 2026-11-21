package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/oauth"
)

func TestMCPClientInteroperability(t *testing.T) {
	sdk := os.Getenv("LISEUR_MCP_SDK")
	if sdk == "" {
		t.Skip("set LISEUR_MCP_SDK to an installed MCP TypeScript SDK")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the MCP client check")
	}
	f := newFolderFixture(t)
	f.enableMCP(t, nil)
	bookID, _ := f.writeBook(t, "interop.epub", []byte("fixture"))
	access := f.assistantToken(t, f.user.ID, oauth.ScopeLibrary)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "mcp-client.mjs"))
	cmd.Env = append(os.Environ(), "MCP_ENDPOINT="+f.ts.URL+"/mcp", "MCP_ACCESS="+access, "MCP_BOOK_ID="+bookID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("MCP SDK client: %v\n%s", err, out)
	}
}
