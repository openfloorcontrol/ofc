package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpCallRe matches mcp=<server>.<tool> with optional JSON arguments
// right after, e.g. mcp=tasks.add_task{"title":"x"}.
var mcpCallRe = regexp.MustCompile(`mcp=([\w-]+)\.([\w-]+)(\{[^}]*\})?`)

// mcpCalls runs every mcp= call in text against servers and returns one
// "mcp <server>.<tool> -> <result>" line per call; a failed call's
// result is "error: <reason>".
func mcpCalls(ctx context.Context, text string, servers []acp.McpServer) []string {
	var out []string
	for _, m := range mcpCallRe.FindAllStringSubmatch(text, -1) {
		server, tool, args := m[1], m[2], m[3]
		result, err := callMCP(ctx, servers, server, tool, args)
		if err != nil {
			result = "error: " + err.Error()
		}
		out = append(out, fmt.Sprintf("mcp %s.%s -> %s", server, tool, result))
	}
	return out
}

// callMCP calls tool on the MCP server called name. A name the session
// was not given is reached at the address of a given server with its
// last path segment replaced by name, with that server's headers.
func callMCP(ctx context.Context, servers []acp.McpServer, name, tool, args string) (string, error) {
	if len(servers) == 0 {
		return "", fmt.Errorf("no MCP servers")
	}
	target, given := servers[0], false
	for _, s := range servers {
		if serverName(s) == name {
			target, given = s, true
			break
		}
	}
	url, sse, headers := serverAddress(target)
	if !given {
		url = replaceLastSegment(url, serverName(target), name)
	}

	client := &http.Client{Transport: headerTransport(headers)}
	var transport mcp.Transport
	if sse {
		transport = &mcp.SSEClientTransport{Endpoint: url, HTTPClient: client}
	} else {
		transport = &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: client}
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "fakeagent", Version: "1"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return "", err
	}
	defer session.Close()

	var arguments map[string]any
	if args != "" {
		if err := json.Unmarshal([]byte(args), &arguments); err != nil {
			return "", fmt.Errorf("arguments: %w", err)
		}
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		return "", err
	}
	var texts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, t.Text)
		}
	}
	return strings.Join(texts, " "), nil
}

func serverName(s acp.McpServer) string {
	switch {
	case s.Sse != nil:
		return s.Sse.Name
	case s.Http != nil:
		return s.Http.Name
	}
	return ""
}

func serverAddress(s acp.McpServer) (url string, sse bool, headers []acp.HttpHeader) {
	switch {
	case s.Sse != nil:
		return s.Sse.Url, true, s.Sse.Headers
	case s.Http != nil:
		return s.Http.Url, false, s.Http.Headers
	}
	return "", false, nil
}

// replaceLastSegment replaces the last occurrence of /old in url by /new.
func replaceLastSegment(url, old, new string) string {
	i := strings.LastIndex(url, "/"+old)
	if i < 0 {
		return url
	}
	return url[:i] + "/" + new + url[i+1+len(old):]
}

type headerTransport []acp.HttpHeader

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for _, hd := range h {
		r.Header.Set(hd.Name, hd.Value)
	}
	return http.DefaultTransport.RoundTrip(r)
}
