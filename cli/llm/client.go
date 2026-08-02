// Package llm provides an OpenAI-compatible LLM client with streaming support.
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
	Content    string     `json:"content"`
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
			Content string `json:"content"`
			// Servers that separate reasoning server-side put it here.
			// reasoning_content is DeepSeek/vLLM; reasoning is OpenRouter.
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
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
	Reasoning string
	ToolCalls []ToolCall
}

// StreamHandler receives the response as it arrives. Reasoning is delivered
// separately from answer content so callers never have to unpick one from the
// other; either callback may be nil.
type StreamHandler struct {
	OnToken   func(string)
	OnThought func(string)
}

// Client is an OpenAI-compatible API client
type Client struct {
	Endpoint string
	APIKey   string

	// Thinking controls reasoning separation. The zero value means auto.
	Thinking Thinking
}

// NewClient creates a new LLM client
func NewClient(endpoint, apiKey string) *Client {
	return &Client{
		Endpoint: strings.TrimSuffix(endpoint, "/"),
		APIKey:   apiKey,
	}
}

// ChatStream sends a chat request and streams the response
func (c *Client) ChatStream(model string, messages []Message, temperature float64, tools []Tool, h StreamHandler) (*ChatResult, error) {
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
	var fullContent, fullReasoning strings.Builder
	toolCalls := make(map[int]*ToolCall) // Index -> ToolCall
	reader := bufio.NewReader(resp.Body)

	thinking := c.Thinking.normalize()
	onContent := func(s string) {
		fullContent.WriteString(s)
		if h.OnToken != nil {
			h.OnToken(s)
		}
	}
	onThought := func(s string) {
		fullReasoning.WriteString(s)
		if h.OnThought != nil {
			h.OnThought(s)
		}
	}
	var split *splitter
	if thinking.scansTags() {
		split = &splitter{openTag: thinking.OpenTag, closeTag: thinking.CloseTag}
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return &ChatResult{
				Content:   fullContent.String(),
				Reasoning: fullReasoning.String(),
			}, err
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

			// Reasoning the server already separated for us
			if thinking.readsField() {
				if r := delta.ReasoningContent; r != "" {
					onThought(r)
				} else if r := delta.Reasoning; r != "" {
					onThought(r)
				}
			}

			// Handle content
			if delta.Content != "" {
				if split != nil {
					split.Write(delta.Content, onContent, onThought)
				} else {
					onContent(delta.Content)
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

	if split != nil {
		split.Close(onContent, onThought)
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
		Reasoning: fullReasoning.String(),
		ToolCalls: resultToolCalls,
	}, nil
}
