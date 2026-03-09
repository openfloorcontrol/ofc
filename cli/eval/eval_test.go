package eval

import (
	"testing"
)

func TestParseResult(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantScore int
		wantErr   bool
	}{
		{
			name:      "plain JSON",
			input:     `{"score": 4, "reasoning": "Good analysis"}`,
			wantScore: 4,
		},
		{
			name:      "with markdown fences",
			input:     "```json\n{\"score\": 3, \"reasoning\": \"Decent\"}\n```",
			wantScore: 3,
		},
		{
			name:      "with plain fences",
			input:     "```\n{\"score\": 5, \"reasoning\": \"Excellent\"}\n```",
			wantScore: 5,
		},
		{
			name:      "with whitespace",
			input:     "  \n{\"score\": 2, \"reasoning\": \"Needs work\"}  \n",
			wantScore: 2,
		},
		{
			name:    "invalid JSON",
			input:   "this is not json",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseResult(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Score != tt.wantScore {
				t.Errorf("score = %d, want %d", result.Score, tt.wantScore)
			}
			if result.Reasoning == "" {
				t.Error("reasoning should not be empty")
			}
		})
	}
}
