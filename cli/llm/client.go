package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Message represents a chat message
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool invocation
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Tool definition for the API
type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Parameters  map[string]interface{} `json:"parameters"`
	} `json:"function"`
}

// BashTool is the tool definition for bash execution
var BashTool = Tool{
	Type: "function",
}

func init() {
	BashTool.Function.Name = "bash"
	BashTool.Function.Description = "Run a bash command in the workspace container."
	BashTool.Function.Parameters = map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"cmd": map[string]interface{}{
				"type":        "string",
				"description": "The bash command to execute",
			},
		},
		"required": []string{"cmd"},
	}
}

// ChatRequest is the request to the chat API
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
	Tools       []Tool    `json:"tools,omitempty"`
}

// ChatResponse is a non-streaming response
type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// StreamChunk is a chunk from streaming response
type StreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// ChatResult contains the response and any tool calls
type ChatResult struct {
	Content   string
	ToolCalls []ToolCall
}

// Client is an OpenAI-compatible API client
type Client struct {
	Endpoint string
	APIKey   string
}

// NewClient creates a new LLM client
func NewClient(endpoint, apiKey string) *Client {
	return &Client{
		Endpoint: strings.TrimSuffix(endpoint, "/"),
		APIKey:   apiKey,
	}
}

// ChatStream sends a chat request and streams the response
func (c *Client) ChatStream(model string, messages []Message, temperature float64, tools []Tool, onToken func(string)) (*ChatResult, error) {
	req := ChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		Stream:      true,
		Tools:       tools,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", c.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Parse SSE stream
	var fullContent strings.Builder
	toolCalls := make(map[int]*ToolCall) // Index -> ToolCall
	reader := bufio.NewReader(resp.Body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return &ChatResult{Content: fullContent.String()}, err
		}

		line = strings.TrimSpace(line)
		if line == "" || line == "data: [DONE]" {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta

			// Handle content
			if delta.Content != "" {
				fullContent.WriteString(delta.Content)
				if onToken != nil {
					onToken(delta.Content)
				}
			}

			// Handle tool calls
			for _, tc := range delta.ToolCalls {
				if _, exists := toolCalls[tc.Index]; !exists {
					toolCalls[tc.Index] = &ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
					}
				}
				call := toolCalls[tc.Index]
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Type != "" {
					call.Type = tc.Type
				}
				if tc.Function.Name != "" {
					call.Function.Name = tc.Function.Name
				}
				call.Function.Arguments += tc.Function.Arguments
			}
		}
	}

	// Convert tool calls map to slice
	var resultToolCalls []ToolCall
	for i := 0; i < len(toolCalls); i++ {
		if tc, ok := toolCalls[i]; ok {
			resultToolCalls = append(resultToolCalls, *tc)
		}
	}

	return &ChatResult{
		Content:   fullContent.String(),
		ToolCalls: resultToolCalls,
	}, nil
}
