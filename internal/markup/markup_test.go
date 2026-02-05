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

func TestMarkdownToHTML_XSSPrevention(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		mustNot  []string // these substrings must NOT appear (unescaped XSS)
		must     []string // these substrings MUST appear (escaped versions)
	}{
		{
			name:    "plain script tag",
			input:   `<script>alert('xss')</script>`,
			mustNot: []string{"<script>", "</script>"},
			must:    []string{"&lt;script&gt;", "&lt;/script&gt;"},
		},
		{
			name:    "script in bold",
			input:   `**<script>alert('xss')</script>**`,
			mustNot: []string{"<script>", "</script>"},
			must:    []string{"<b>", "&lt;script&gt;"},
		},
		{
			name:    "script in list item",
			input:   `- <script>alert('xss')</script>`,
			mustNot: []string{"<script>", "</script>"},
			must:    []string{"<ul>", "<li>", "&lt;script&gt;"},
		},
		{
			name:    "script in header",
			input:   `# <script>alert('xss')</script>`,
			mustNot: []string{"<script>", "</script>"},
			must:    []string{"<h1>", "&lt;script&gt;"},
		},
		{
			name:    "script in code block",
			input:   "```<script>alert('xss')</script>```",
			mustNot: []string{"<script>alert"},
			must:    []string{"<pre>", "&lt;script&gt;"},
		},
		{
			name:    "img onerror attack",
			input:   `<img src=x onerror=alert('xss')>`,
			mustNot: []string{"<img"}, // the actual XSS vector is the unescaped <img tag
			must:    []string{"&lt;img"},
		},
		{
			name:    "mixed markdown and XSS",
			input:   `Hello **bold** and <script>evil</script> world`,
			mustNot: []string{"<script>", "</script>"},
			must:    []string{"<b>bold</b>", "&lt;script&gt;"},
		},
		{
			name:    "nested list with XSS",
			input:   "- item1 <b>fake</b>\n- item2 **real**",
			mustNot: []string{"<b>fake</b>"},                                  // user's <b> should be escaped
			must:    []string{"&lt;b&gt;fake&lt;/b&gt;", "<b>real</b>", "<li>"}, // markdown ** becomes real <b>
		},
		{
			name:    "ampersand escaping",
			input:   `Tom & Jerry`,
			mustNot: []string{},
			must:    []string{"Tom &amp; Jerry"},
		},
		{
			name:    "quote escaping",
			input:   `He said "hello"`,
			mustNot: []string{},
			must:    []string{"&quot;hello&quot;"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MarkdownToHTML(tt.input)

			for _, bad := range tt.mustNot {
				if strings.Contains(result, bad) {
					t.Errorf("XSS vulnerability: output contains %q\nInput: %s\nOutput: %s", bad, tt.input, result)
				}
			}

			for _, good := range tt.must {
				if !strings.Contains(result, good) {
					t.Errorf("Expected output to contain %q\nInput: %s\nOutput: %s", good, tt.input, result)
				}
			}
		})
	}
}

func TestMarkdownToHTML_NoDoubleEscaping(t *testing.T) {
	// Ensure we don't double-escape (e.g., &amp;lt; instead of &lt;)
	tests := []struct {
		name    string
		input   string
		mustNot []string
	}{
		{
			name:    "plain text with angle bracket",
			input:   `a < b`,
			mustNot: []string{"&amp;lt;"},
		},
		{
			name:    "bold with angle bracket",
			input:   `**a < b**`,
			mustNot: []string{"&amp;lt;"},
		},
		{
			name:    "list item with angle bracket",
			input:   `- a < b`,
			mustNot: []string{"&amp;lt;"},
		},
		{
			name:    "nested list with bold and angle",
			input:   `- **a < b**`,
			mustNot: []string{"&amp;lt;", "&amp;amp;"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MarkdownToHTML(tt.input)

			for _, bad := range tt.mustNot {
				if strings.Contains(result, bad) {
					t.Errorf("Double-escaping detected: output contains %q\nInput: %s\nOutput: %s", bad, tt.input, result)
				}
			}
		})
	}
}
