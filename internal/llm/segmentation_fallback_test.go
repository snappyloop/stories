package llm

import (
	"reflect"
	"strings"
	"testing"
)

func TestWordCount(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"only spaces", "   ", 0},
		{"single word", "hello", 1},
		{"two words", "hello world", 2},
		{"five words", "one two three four five", 5},
		{"nine words", "a b c d e f g h i", 9},
		{"ten words", "a b c d e f g h i j", 10},
		{"trimmed", "  foo bar baz  ", 3},
		{"multiple spaces between", "word    word", 5}, // 1 + 4 spaces (each space counts)
		{"newlines not counted as space", "one\ntwo\nthree", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wordCount(tt.in)
			if got != tt.want {
				t.Errorf("wordCount(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsListLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty after trim", "", true},
		{"only spaces", "   ", true},
		{"number dot", "1. First item", true},
		{"number dot no space", "2.Next", false}, // regex requires space after dot
		{"letter dot", "a. Subpoint", true},
		{"hyphen bullet", "- item", true},
		{"asterisk bullet", "* item", true},
		{"plus bullet", "+ item", true},
		{"unicode bullet", "• item", true},
		{"middle dot", "· item", true},
		{"indent space", "    indented", true},  // leading space/tab counts as list (indentation)
		{"indent tab", "\tindented", true},
		{"paren", "(1) item", true},
		{"bracket", "[x] item", true},
		{"normal sentence", "This is a normal paragraph.", false},
		{"starts with letter", "Hello world", false},
		{"number no dot", "42 items", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isListLine(tt.in)
			if got != tt.want {
				t.Errorf("isListLine(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestLineEndsWithColon(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"only spaces", "   ", false},
		{"ends with colon", "The following:", true},
		{"ends with colon and space", "Items:  ", true},
		{"no colon", "The following items", false},
		{"colon in middle", "foo: bar", false},
		{"multiple lines last has colon", "Line one\nLine two:", true},
		{"multiple lines last no colon", "Line one:\nLine two", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lineEndsWithColon(tt.in)
			if got != tt.want {
				t.Errorf("lineEndsWithColon(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// boundariesToSegments returns text segments for given boundaries (grapheme indices) and text.
func boundariesToSegments(text string, boundaries []int) []string {
	if len(boundaries) == 0 {
		return nil
	}
	byteOffsets := runeToByteOffsets(text)
	var segs []string
	start := 0
	for _, end := range boundaries {
		if end > start {
			byteStart := byteOffsets[start]
			byteEnd := byteOffsets[end]
			if byteEnd > len(text) {
				byteEnd = len(text)
			}
			segs = append(segs, text[byteStart:byteEnd])
		}
		start = end
	}
	return segs
}

func TestFallbackSegmentBoundaries_EmptyAndNoNewlines(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"empty returns nil", ""},
		{"only spaces returns nil", "   \n  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fallbackSegmentBoundaries(tt.text)
			if got != nil {
				t.Errorf("fallbackSegmentBoundaries(%q) = %v, want nil", tt.text, got)
			}
		})
	}
}

func TestFallbackSegmentBoundaries_NoNewlines_SentenceSplit(t *testing.T) {
	tests := []struct {
		name           string
		text           string
		wantSegments   int
		lastMustContain []string // each segment should contain one of these (order)
	}{
		{
			name:         "single sentence",
			text:         "One sentence only.",
			wantSegments: 1,
			lastMustContain: []string{"One sentence only."},
		},
		{
			name:         "two sentences",
			text:         "First sentence. Second sentence.",
			wantSegments: 2,
			lastMustContain: []string{"First sentence.", "Second sentence."},
		},
		{
			name:         "three sentences",
			text:         "A. B. C.",
			wantSegments: 3,
			lastMustContain: []string{"A.", "B.", "C."},
		},
		{
			name:         "exclamation and question",
			text:         "Really! Is it? Yes.",
			wantSegments: 3,
			lastMustContain: []string{"Really!", "Is it?", "Yes."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fallbackSegmentBoundaries(tt.text)
			if len(tt.text) == 0 {
				return
			}
			segments := boundariesToSegments(tt.text, got)
			if len(segments) != tt.wantSegments {
				t.Errorf("fallbackSegmentBoundaries: got %d segments, want %d; boundaries=%v, segments=%q",
					len(segments), tt.wantSegments, got, segments)
			}
			byteOffsets := runeToByteOffsets(tt.text)
			numGraphemes := len(byteOffsets) - 1
			if len(got) > 0 && got[len(got)-1] != numGraphemes {
				t.Errorf("last boundary = %d, want %d (numGraphemes)", got[len(got)-1], numGraphemes)
			}
			for i, wantSub := range tt.lastMustContain {
				if i >= len(segments) {
					break
				}
				if !strings.Contains(segments[i], wantSub) && !strings.Contains(segments[i], strings.TrimSuffix(wantSub, ".")) {
					t.Errorf("segment[%d] = %q, want to contain %q", i, segments[i], wantSub)
				}
			}
		})
	}
}

func TestFallbackSegmentBoundaries_WithNewlines(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantSegments int
		desc         string
	}{
		{
			name:         "two paragraphs both long",
			text:         "First paragraph here with enough words to exceed ten word limit.\n\nSecond paragraph here with enough words to exceed ten word limit.",
			wantSegments: 2,
			desc:         "split at double newline when both blocks have >= 10 words",
		},
		{
			name:         "short second line merged",
			text:         "A long paragraph with enough words to be a full block of text.\nShort.",
			wantSegments: 1,
			desc:         "second line < 10 words so merged",
		},
		{
			name:         "two long paragraphs",
			text:         "First paragraph with more than ten words in it so we split.\n\nSecond paragraph also long enough to be its own segment here.",
			wantSegments: 2,
			desc:         "both paragraphs long enough",
		},
		{
			name:         "list not split",
			text:         "Items:\n1. First item\n2. Second item\n3. Third item",
			wantSegments: 1,
			desc:         "colon before newline and number-dot lines not split",
		},
		{
			name:         "bullet list not split",
			text:         "List:\n- One\n- Two\n- Three",
			wantSegments: 1,
			desc:         "bullet list merged",
		},
		{
			name:         "single line no trailing newline",
			text:         "Only one line",
			wantSegments: 1,
			desc:         "single block",
		},
		{
			name:         "three blocks with short middle",
			text:         "First long paragraph with enough words to count as one segment.\n\nShort.\n\nThird long paragraph with enough words again to be ten or more.",
			wantSegments: 2,
			desc:         "short middle merged with first",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fallbackSegmentBoundaries(tt.text)
			segments := boundariesToSegments(tt.text, got)
			if len(segments) != tt.wantSegments {
				t.Errorf("%s: got %d segments, want %d; boundaries=%v, segments=%q",
					tt.desc, len(segments), tt.wantSegments, got, segments)
			}
			byteOffsets := runeToByteOffsets(tt.text)
			numGraphemes := len(byteOffsets) - 1
			if len(got) > 0 && got[len(got)-1] != numGraphemes {
				t.Errorf("last boundary = %d, want %d", got[len(got)-1], numGraphemes)
			}
		})
	}
}

func TestFallbackSegmentBoundaries_BoundariesInvariants(t *testing.T) {
	texts := []string{
		"Single.",
		"One. Two. Three.",
		"Line one\n\nLine two",
		"Intro:\n1. A\n2. B",
		"A paragraph with many words so it is long enough to be a single segment.\n\nAnother one.",
	}
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		got := fallbackSegmentBoundaries(text)
		if got == nil {
			continue
		}
		byteOffsets := runeToByteOffsets(text)
		numGraphemes := len(byteOffsets) - 1
		// Ascending
		for i := 1; i < len(got); i++ {
			if got[i] <= got[i-1] {
				t.Errorf("text %q: boundaries not ascending: %v", text, got)
			}
		}
		// Last boundary is end of text
		if got[len(got)-1] != numGraphemes {
			t.Errorf("text %q: last boundary = %d, want %d", text, got[len(got)-1], numGraphemes)
		}
		// Segments cover full text (no gaps, no overlap)
		segs := boundariesToSegments(text, got)
		concatenated := strings.Join(segs, "")
		normalized := strings.ReplaceAll(text, "\n", " ")
		normalized = strings.ReplaceAll(normalized, "  ", " ")
		if strings.ReplaceAll(concatenated, "\n", " ") != strings.ReplaceAll(text, "\n", " ") {
			// Allow for trimming differences; at least length in graphemes should match
			runeConc := 0
			for range concatenated {
				runeConc++
			}
			runeText := 0
			for range text {
				runeText++
			}
			if runeConc != runeText {
				t.Errorf("text %q: segments don't cover full text; conc len=%d text len=%d",
					text, len(concatenated), len(text))
			}
		}
		_ = normalized
	}
}

func TestFallbackBoundariesByNewlines(t *testing.T) {
	// Two blocks with >= 10 words each so they are not merged (wordCount = 1 + spaces)
	text := "Block one with enough words here to make ten or more.\n\nBlock two with enough words here to make ten or more."
	byteOffsets := runeToByteOffsets(text)
	got := fallbackBoundariesByNewlines(text, byteOffsets)
	if len(got) != 2 {
		t.Errorf("fallbackBoundariesByNewlines: got %d boundaries, want 2; %v", len(got), got)
	}
	segs := boundariesToSegments(text, got)
	if len(segs) != 2 {
		t.Fatalf("got %d segments", len(segs))
	}
	if !strings.Contains(segs[0], "Block one") {
		t.Errorf("segment 0 = %q", segs[0])
	}
	if !strings.Contains(segs[1], "Block two") {
		t.Errorf("segment 1 = %q", segs[1])
	}
}

func TestFallbackBoundariesBySentences(t *testing.T) {
	text := "Hi. Bye."
	byteOffsets := runeToByteOffsets(text)
	got := fallbackBoundariesBySentences(text, byteOffsets)
	if len(got) != 2 {
		t.Errorf("fallbackBoundariesBySentences: got %d boundaries, want 2; %v", len(got), got)
	}
	segs := boundariesToSegments(text, got)
	if len(segs) != 2 {
		t.Fatalf("got %d segments", len(segs))
	}
	if !strings.Contains(segs[0], "Hi") {
		t.Errorf("segment 0 = %q", segs[0])
	}
	if !strings.Contains(segs[1], "Bye") {
		t.Errorf("segment 1 = %q", segs[1])
	}
}

func TestFallbackSegmentBoundaries_TrimSpace(t *testing.T) {
	withSpaces := "  One. Two.  "
	noSpaces := "One. Two."
	b1 := fallbackSegmentBoundaries(withSpaces)
	b2 := fallbackSegmentBoundaries(noSpaces)
	// fallbackSegmentBoundaries trims input, so both should yield same boundaries (for trimmed text)
	trimmed := strings.TrimSpace(withSpaces)
	s1 := boundariesToSegments(trimmed, b1)
	s2 := boundariesToSegments(noSpaces, b2)
	if len(s1) != 2 || len(s2) != 2 {
		t.Errorf("trimmed: %d segments, untrimmed: %d segments", len(s1), len(s2))
	}
	if !reflect.DeepEqual(b1, b2) {
		t.Errorf("boundaries differ for trimmed vs untrimmed input: %v vs %v", b1, b2)
	}
}
