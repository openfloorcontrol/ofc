package llm

import (
	"strings"
	"testing"
)

// feed runs chunks through a splitter and returns the accumulated content and
// reasoning, including whatever Close flushes.
func feed(chunks ...string) (content, thought string) {
	var c, th strings.Builder
	s := &splitter{openTag: DefaultOpenTag, closeTag: DefaultCloseTag}
	onC := func(x string) { c.WriteString(x) }
	onT := func(x string) { th.WriteString(x) }
	for _, chunk := range chunks {
		s.Write(chunk, onC, onT)
	}
	s.Close(onC, onT)
	return c.String(), th.String()
}

func TestSplitter(t *testing.T) {
	tests := []struct {
		name    string
		chunks  []string
		content string
		thought string
	}{
		{
			name:    "no tags",
			chunks:  []string{"just an answer"},
			content: "just an answer",
		},
		{
			name:    "whole block in one chunk",
			chunks:  []string{"<think>reasoning</think>answer"},
			content: "answer",
			thought: "reasoning",
		},
		{
			name:    "text before and after",
			chunks:  []string{"before<think>why</think>after"},
			content: "beforeafter",
			thought: "why",
		},
		{
			// The case that motivated the hand-rolled scanner: a tag
			// straddling a chunk boundary must not leak into content.
			name:    "open tag split across chunks",
			chunks:  []string{"<th", "ink>reasoning</think>answer"},
			content: "answer",
			thought: "reasoning",
		},
		{
			name:    "close tag split across chunks",
			chunks:  []string{"<think>reasoning</thi", "nk>answer"},
			content: "answer",
			thought: "reasoning",
		},
		{
			name:    "tag split one byte at a time",
			chunks:  []string{"<", "t", "h", "i", "n", "k", ">", "r", "</think>", "a"},
			content: "a",
			thought: "r",
		},
		{
			// Cut off mid-thought: the remainder is reasoning, not an answer.
			name:    "unterminated block",
			chunks:  []string{"<think>started thinking and then"},
			thought: "started thinking and then",
		},
		{
			name:    "lone angle bracket is content",
			chunks:  []string{"a < b and c > d"},
			content: "a < b and c > d",
		},
		{
			// A partial tag that never completes must be flushed as content.
			name:    "partial tag that never completes",
			chunks:  []string{"answer <thi"},
			content: "answer <thi",
		},
		{
			name:    "near miss tag",
			chunks:  []string{"<thinking>not our tag</thinking>"},
			content: "<thinking>not our tag</thinking>",
		},
		{
			name:    "two blocks",
			chunks:  []string{"<think>one</think>mid<think>two</think>end"},
			content: "midend",
			thought: "onetwo",
		},
		{
			name:    "empty block",
			chunks:  []string{"<think></think>answer"},
			content: "answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, thought := feed(tt.chunks...)
			if content != tt.content {
				t.Errorf("content = %q, want %q", content, tt.content)
			}
			if thought != tt.thought {
				t.Errorf("thought = %q, want %q", thought, tt.thought)
			}
		})
	}
}

func TestPartialSuffix(t *testing.T) {
	tests := []struct {
		s, tag string
		want   int
	}{
		{"abc", "<think>", 0},
		{"abc<", "<think>", 1},
		{"abc<th", "<think>", 3},
		{"<think", "<think>", 6},
		{"", "<think>", 0},
		{"<<", "<think>", 1},
	}
	for _, tt := range tests {
		if got := partialSuffix(tt.s, tt.tag); got != tt.want {
			t.Errorf("partialSuffix(%q, %q) = %d, want %d", tt.s, tt.tag, got, tt.want)
		}
	}
}

func TestThinkingNormalize(t *testing.T) {
	got := Thinking{}.normalize()
	if got.Mode != ModeAuto || got.OpenTag != DefaultOpenTag || got.CloseTag != DefaultCloseTag {
		t.Errorf("zero value normalized to %+v, want auto with default tags", got)
	}

	custom := Thinking{Mode: ModeTags, OpenTag: "◁think▷", CloseTag: "◁/think▷"}.normalize()
	if custom.OpenTag != "◁think▷" {
		t.Errorf("custom tags were overwritten: %+v", custom)
	}

	if (Thinking{Mode: ModeNone}).normalize().scansTags() {
		t.Error("ModeNone should not scan tags")
	}
	if (Thinking{Mode: ModeField}).normalize().scansTags() {
		t.Error("ModeField should not scan tags")
	}
	if (Thinking{Mode: ModeTags}).normalize().readsField() {
		t.Error("ModeTags should not read the delta field")
	}
}
