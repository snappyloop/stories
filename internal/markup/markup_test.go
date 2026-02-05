package markup

import (
	"strings"
	"testing"
)

func TestToHTML_SourceBlockWithEscapedQuotes(t *testing.T) {
	// Test case: filename contains a quote character, formatted with %q
	// This produces filename="test\"file.pdf" which the regex must handle
	markup := `[[SOURCE file_id=abc123 filename="test\"file.pdf"]]
This is extracted content from a file.
[[/SOURCE]]

[[SEGMENT id=seg-1]]
Hello world
[[/SEGMENT]]
`
	result := ToHTML(markup, "job-123")

	// SOURCE block content should be excluded from output
	if strings.Contains(result, "extracted content") {
		t.Errorf("SOURCE block content should be excluded, but found in output:\n%s", result)
	}
	if strings.Contains(result, "[[SOURCE") {
		t.Errorf("SOURCE tag should be excluded, but found in output:\n%s", result)
	}

	// SEGMENT content should still be present
	if !strings.Contains(result, "Hello world") {
		t.Errorf("SEGMENT content should be present, but not found in output:\n%s", result)
	}
}

func TestToHTML_SourceBlockSimpleFilename(t *testing.T) {
	// Test case: simple filename without special characters
	markup := `[[SOURCE file_id=abc123 filename="simple.pdf"]]
Extracted text here.
[[/SOURCE]]

[[SEGMENT id=seg-1]]
Segment text
[[/SEGMENT]]
`
	result := ToHTML(markup, "job-123")

	if strings.Contains(result, "Extracted text") {
		t.Errorf("SOURCE block content should be excluded, but found in output:\n%s", result)
	}
	if !strings.Contains(result, "Segment text") {
		t.Errorf("SEGMENT content should be present, but not found in output:\n%s", result)
	}
}

func TestToHTML_SourceBlockWithBackslash(t *testing.T) {
	// Test case: filename contains backslash (escaped as \\)
	markup := `[[SOURCE file_id=abc123 filename="path\\to\\file.pdf"]]
Content from file.
[[/SOURCE]]

[[SEGMENT id=seg-1]]
More content
[[/SEGMENT]]
`
	result := ToHTML(markup, "job-123")

	if strings.Contains(result, "Content from file") {
		t.Errorf("SOURCE block content should be excluded, but found in output:\n%s", result)
	}
	if !strings.Contains(result, "More content") {
		t.Errorf("SEGMENT content should be present, but not found in output:\n%s", result)
	}
}
