package llm

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// fallbackSegmentBoundaries returns grapheme boundaries (same format as LLM) using rule-based
// segmentation. Used when LLM segmentation fails. Result must not be cached.
//
// Rules:
// - If text has newlines: segment by newline(s), but do not split if the block after a newline
//   has < 10 words (count spaces), or if it looks like a list item (starts with number+dot,
//   indent, bullet), or if the line before the newline ends with a colon.
// - If text has no newlines: segment by sentence (.). Return boundaries as grapheme indices.
func fallbackSegmentBoundaries(text string) []int {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	byteOffsets := runeToByteOffsets(text)
	numGraphemes := len(byteOffsets) - 1

	if strings.Contains(text, "\n") {
		boundaries := fallbackBoundariesByNewlines(text, byteOffsets)
		if len(boundaries) > 0 {
			return boundaries
		}
	}
	// No newlines or no boundaries from newline logic: segment by sentences (dots)
	boundaries := fallbackBoundariesBySentences(text, byteOffsets)
	if len(boundaries) == 0 {
		return []int{numGraphemes}
	}
	return boundaries
}

// wordCount returns approximate word count (1 + number of spaces in trimmed s; 0 if empty).
func wordCount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n := 1
	for _, r := range s {
		if r == ' ' {
			n++
		}
	}
	return n
}

// isListLine reports whether the line (after trim) looks like a list item: starts with number+dot,
// indentation (space/tab), or bullet (-, *, •, ·, +, etc.), or parenthesized/dotted number.
var (
	reListNumberDot = regexp.MustCompile(`^\d+\.\s`)
	reListLetterDot = regexp.MustCompile(`^[a-zA-Z]\.\s`)
)

func isListLine(s string) bool {
	// Detect indentation (leading space/tab) before trimming
	if s != "" {
		first, _ := utf8.DecodeRuneInString(s)
		if first == ' ' || first == '\t' {
			return true
		}
	}
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return true // all-whitespace counts as continuation
	}
	if reListNumberDot.MatchString(s) || reListLetterDot.MatchString(s) {
		return true
	}
	first, _ := utf8.DecodeRuneInString(s)
	// Bullets
	switch first {
	case '-', '*', '•', '·', '+', '‐', '–', '—':
		return true
	case '(', '[', '●', '○', '▪', '▫':
		return true
	default:
		return false
	}
}

// lineEndsWithColon reports whether the last line in the given text (paragraph) ends with a colon.
func lineEndsWithColon(paragraph string) bool {
	paragraph = strings.TrimRight(paragraph, " \t\n")
	if len(paragraph) == 0 {
		return false
	}
	return paragraph[len(paragraph)-1] == ':'
}

func fallbackBoundariesByNewlines(text string, byteOffsets []int) []int {
	// Split by lines (paragraphs separated by one or more newlines)
	type block struct{ start, end int }
	var blocks []block
	pos := 0
	for pos < len(text) {
		start := pos
		for pos < len(text) && text[pos] != '\n' {
			pos++
		}
		end := pos
		if end > start {
			blocks = append(blocks, block{start, end})
		}
		for pos < len(text) && text[pos] == '\n' {
			pos++
		}
	}
	if len(blocks) == 0 {
		return nil
	}

	// Merge blocks that should not be split: < 10 words, list line, or previous line ends with colon
	var segmentEnds []int
	segmentEndByte := blocks[0].end
	for i := 1; i < len(blocks); i++ {
		b := blocks[i]
		content := text[b.start:b.end]
		prevContent := text[blocks[i-1].start:blocks[i-1].end]
		merge := wordCount(content) < 10 ||
			isListLine(content) ||
			lineEndsWithColon(prevContent)
		if merge {
			segmentEndByte = b.end
			continue
		}
		segmentEnds = append(segmentEnds, segmentEndByte)
		segmentEndByte = b.end
	}
	segmentEnds = append(segmentEnds, segmentEndByte)

	numGraphemes := len(byteOffsets) - 1
	boundaries := make([]int, 0, len(segmentEnds))
	for _, byteEnd := range segmentEnds {
		g := findGraphemeForBytePos(byteOffsets, byteEnd)
		if len(boundaries) == 0 || g > boundaries[len(boundaries)-1] {
			boundaries = append(boundaries, g)
		}
	}
	if len(boundaries) > 0 && boundaries[len(boundaries)-1] != numGraphemes {
		boundaries = append(boundaries, numGraphemes)
	}
	return boundaries
}

func fallbackBoundariesBySentences(text string, byteOffsets []int) []int {
	numGraphemes := len(byteOffsets) - 1
	var boundaries []int
	i := 0
	for i < len(text) {
		// Find next sentence end (. ! ?)
		j := strings.IndexAny(text[i:], ".!?")
		if j < 0 {
			break
		}
		i += j
		// Skip trailing quote/paren/space after the punctuation
		i++
		for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '"' || text[i] == ')' || text[i] == '\'') {
			i++
		}
		boundaries = append(boundaries, findGraphemeForBytePos(byteOffsets, i))
	}
	if len(boundaries) > 0 && boundaries[len(boundaries)-1] != numGraphemes {
		boundaries = append(boundaries, numGraphemes)
	}
	return boundaries
}
