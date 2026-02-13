package mcpclient

import (
  "context"
  "log"
  "sync"
  "time"

  "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPTools struct {
  Name        string         `json:"name"`
  Description string         `json:"description"`
  Parameters  map[string]any `json:"parameters"`
}

var (
  mcpClient  *mcp.Client
  mcpSession *mcp.ClientSession
  mcpTools   []MCPTools
  once       sync.Once
  initErr    error
)

func GetClient(httpURL string) (*mcp.ClientSession, *mcp.Client, []MCPTools, error) {
  once.Do(func() {
    mcpClient = mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)

    mcpSession, mcpTools, initErr = initClient(mcpClient, httpURL)
  })
  return mcpSession, mcpClient, mcpTools, initErr
}

func initClient(mcpClient *mcp.Client, httpURL string) (*mcp.ClientSession, []MCPTools, error) {

  // Create a context with timeout
  ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
  defer cancel()

  // Create client with the transport
  // Connect to a server over stdin/stdout.
  session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpURL}, nil)
  if err != nil {
    log.Fatal(err)
  }
  mcpTools := make([]MCPTools, 0)
  tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
  for _, tool := range tools.Tools {
    mcpTools = append(mcpTools, MCPTools{
      Name:        tool.Name,
      Description: tool.Description,
      Parameters:  tool.InputSchema.(map[string]any),
    })
    log.Printf("Tool: %v", tool)
  }
  return session, mcpTools, nil
}
