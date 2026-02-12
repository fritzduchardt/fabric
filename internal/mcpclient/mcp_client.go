package mcpclient

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type MCPTools struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

var (
	mcpClient *client.Client
	mcpTools  []MCPTools
	once      sync.Once
	initErr   error
)

func GetClient(httpURL string) (*client.Client, []MCPTools, error) {
	once.Do(func() {
		mcpClient, mcpTools, initErr = initClient(httpURL)
	})
	return mcpClient, mcpTools, initErr
}

func initClient(httpURL string) (*client.Client, []MCPTools, error) {

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create client based on transport type
	var c *client.Client
	var err error

	fmt.Println("Initializing HTTP client...")
	// Create HTTP transport
	httpTransport, err := transport.NewStreamableHTTP(httpURL)
	// NOTE: the default streamableHTTP transport is not 100% identical to the stdio client.
	// By default, it could not receive global notifications (e.g. toolListChanged).
	// You need to enable the `WithContinuousListening()` option to establish a long-live connection,
	// and receive the notifications any time the server sends them.
	//
	//   httpTransport, err := transport.NewStreamableHTTP(*httpURL, transport.WithContinuousListening())
	if err != nil {
		log.Printf("Failed to create HTTP transport: %v", err)
		return nil, nil, err
	}

	// Create client with the transport
	c = client.NewClient(httpTransport)

	// Set up notification handler
	c.OnNotification(func(notification mcp.JSONRPCNotification) {
		fmt.Printf("Received notification: %s\n", notification.Method)
	})

	// Initialize the client
	fmt.Println("Initializing client...")
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "Fabric Client",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := c.Initialize(ctx, initRequest)
	if err != nil {
		log.Printf("Failed to initialize: %v", err)
		return nil, nil, err
	}

	// Display server information
	fmt.Printf("Connected to server: %s (version %s)\n",
		serverInfo.ServerInfo.Name,
		serverInfo.ServerInfo.Version)
	fmt.Printf("Server capabilities: %+v\n", serverInfo.Capabilities)

	// Perform health check using ping
	fmt.Println("Performing health check...")
	if err := c.Ping(ctx); err != nil {
		log.Printf("Health check failed: %v", err)
		return nil, nil, err
	}
	fmt.Println("Server is alive and responding")

	// List available tools if the server supports them
	mcpTools := make([]MCPTools, 0)
	if serverInfo.Capabilities.Tools != nil {
		fmt.Println("Fetching available tools...")
		toolsRequest := mcp.ListToolsRequest{}
		toolsResult, err := c.ListTools(ctx, toolsRequest)
		if err != nil {
			log.Printf("Failed to list tools: %v", err)
			return nil, nil, err
		}
		fmt.Printf("Server has %d tools available\n", len(toolsResult.Tools))
		for i, tool := range toolsResult.Tools {
			mcpTools = append(mcpTools, MCPTools{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema.Properties,
			})
			fmt.Printf("  %d. %s - %s\n", i+1, tool.Name, tool.Description)
		}
	}

	// List available resources if the server supports them
	if serverInfo.Capabilities.Resources != nil {
		fmt.Println("Fetching available resources...")
		resourcesRequest := mcp.ListResourcesRequest{}
		resourcesResult, err := c.ListResources(ctx, resourcesRequest)
		if err != nil {
			log.Printf("Failed to list resources: %v", err)
			return nil, nil, err
		}
		fmt.Printf("Server has %d resources available\n", len(resourcesResult.Resources))
		for i, resource := range resourcesResult.Resources {
			fmt.Printf("  %d. %s - %s\n", i+1, resource.URI, resource.Name)
		}
	}
	return c, mcpTools, nil
}
