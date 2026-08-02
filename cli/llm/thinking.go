package llm

import "strings"

// Reasoning models expose their thinking one of two ways: inline in the
// content stream between tags (<think>…</think>), or as a separate field on
// the streaming delta (reasoning_content). Which one you get depends on the
// inference server, not just the model, so both paths are handled and the
// caller usually leaves Mode at auto.

// Mode selects how reasoning is separated from answer content.
type Mode string

const (
	// ModeAuto handles both the delta field and inline tags.
	ModeAuto Mode = "auto"
	// ModeTags only scans for inline tags.
	ModeTags Mode = "tags"
	// ModeField only reads the delta field.
	ModeField Mode = "field"
	// ModeNone treats everything as answer content — for models where the
	// tags are legitimately part of the output.
	ModeNone Mode = "none"
)

// Default tags, as popularized by DeepSeek-R1 and used by most local models.
const (
	DefaultOpenTag  = "<think>"
	DefaultCloseTag = "</think>"
)

// Thinking configures reasoning separation. The zero value means auto with
// the default tags.
type Thinking struct {
	Mode     Mode
	OpenTag  string
	CloseTag string
}

func (t Thinking) normalize() Thinking {
	if t.Mode == "" {
		t.Mode = ModeAuto
	}
	if t.OpenTag == "" {
		t.OpenTag = DefaultOpenTag
	}
	if t.CloseTag == "" {
		t.CloseTag = DefaultCloseTag
	}
	return t
}

func (t Thinking) scansTags() bool  { return t.Mode == ModeAuto || t.Mode == ModeTags }
func (t Thinking) readsField() bool { return t.Mode == ModeAuto || t.Mode == ModeField }

// splitter separates a streamed content run into answer text and reasoning
// text. Tags may straddle chunk boundaries, so bytes that could still turn
// out to be part of a tag are held back until they are resolved either way.
type splitter struct {
	openTag  string
	closeTag string
	inside   bool
	pending  string
}

func emit(f func(string), s string) {
	if f != nil && s != "" {
		f(s)
	}
}

// Write feeds one chunk of content, classifying everything it can.
func (s *splitter) Write(chunk string, onContent, onThought func(string)) {
	s.pending += chunk

	for {
		tag, out := s.closeTag, onThought
		if !s.inside {
			tag, out = s.openTag, onContent
		}

		if i := strings.Index(s.pending, tag); i >= 0 {
			emit(out, s.pending[:i])
			s.pending = s.pending[i+len(tag):]
			s.inside = !s.inside
			continue
		}

		hold := partialSuffix(s.pending, tag)
		emit(out, s.pending[:len(s.pending)-hold])
		s.pending = s.pending[len(s.pending)-hold:]
		return
	}
}

// Close flushes whatever was held back when the stream ends. An unterminated
// block means the model was cut off mid-thought, so the remainder is reasoning
// rather than answer text.
func (s *splitter) Close(onContent, onThought func(string)) {
	if s.pending == "" {
		return
	}
	if s.inside {
		emit(onThought, s.pending)
	} else {
		emit(onContent, s.pending)
	}
	s.pending = ""
}

// partialSuffix returns the length of the longest suffix of s that is also a
// proper prefix of tag — the bytes that cannot yet be classified because they
// might still become a tag.
func partialSuffix(s, tag string) int {
	n := len(tag) - 1
	if len(s) < n {
		n = len(s)
	}
	for ; n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}
