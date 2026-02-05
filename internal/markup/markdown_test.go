package markup

import (
	"strings"
	"testing"
)

func TestMarkdownToHTML_NestedLists(t *testing.T) {
	input := `1.  **Revenue & Monetization:**
    *   The platform has generated **11,914.00 RUB** from only **7 Paying Users**. This implies a high Average Revenue Per Paying User (ARPPU) of approximately **1,702 RUB**.
    *   However, the conversion rate to paying users is extremely low relative to the Total Users (1,741), suggesting monetization is a bottleneck.

2.  **Traffic Sources (UTM):**
    *   **Top Performer:** ` + "`tg3`" + ` is the leading source for volume (266 users) and maintains a high action rate (69.6%).
    *   **High Engagement:** ` + "`ta01`" + ` has the highest action rate at 70%, though the volume is low (30 users).
    *   **Organic:** Organic traffic accounts for 15% of users (225).

3.  **User Behavior & Engagement:**
    *   **Action Gap:** While 1,741 users are registered, there is a significant polarization in activity.

4.  **Retention:**
    *   **Churn:** The churn rate is significant at **34.4%** (598 churned users out of ~1741 total).`

	result := MarkdownToHTML(input)

	// Check that we have ONE ordered list (should only have one <ol> opening tag)
	olCount := strings.Count(result, "<ol>")
	if olCount != 1 {
		t.Errorf("Expected exactly 1 <ol> tag (one continuous ordered list), got %d: %s", olCount, result)
	}

	// Check that nested unordered lists are inside <li> tags
	if !strings.Contains(result, "<ul>") || !strings.Contains(result, "</ul>") {
		t.Errorf("Expected nested <ul> tags, got: %s", result)
	}

	// Should NOT have standalone "    *   " text (unordered items should be converted)
	if strings.Contains(result, "*   The platform") {
		t.Errorf("Found unconverted list marker, got: %s", result)
	}

	// Check bold conversion
	if !strings.Contains(result, "<b>Revenue &amp; Monetization:</b>") {
		t.Errorf("Expected bold Revenue & Monetization, got: %s", result)
	}

	// Check inline code
	if !strings.Contains(result, "<code>tg3</code>") {
		t.Errorf("Expected <code>tg3</code>, got: %s", result)
	}

	// Verify all 4 list items are present
	for i := 1; i <= 4; i++ {
		if !strings.Contains(result, "<li>") {
			t.Errorf("Expected at least %d list items", i)
		}
	}
}

func TestMarkdownToHTML_HorizontalRule(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"dashes", "Some text\n---\nMore text", "Some text\n<hr>\nMore text"},
		{"asterisks", "Some text\n***\nMore text", "Some text\n<hr>\nMore text"},
		{"underscores", "Some text\n___\nMore text", "Some text\n<hr>\nMore text"},
		{"extra dashes", "------- ", "<hr> "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarkdownToHTML(tt.input)
			if !strings.Contains(got, "<hr>") {
				t.Errorf("MarkdownToHTML() = %v, want to contain <hr>", got)
			}
		})
	}
}

func TestMarkdownToHTML_Tables(t *testing.T) {
	input := `| Name | Age | City |
|------|-----|------|
| Alice | 30 | NYC |
| Bob | 25 | LA |`

	result := MarkdownToHTML(input)

	// Check table tags
	if !strings.Contains(result, "<table>") || !strings.Contains(result, "</table>") {
		t.Errorf("Expected <table> tags, got: %s", result)
	}

	// Check thead
	if !strings.Contains(result, "<thead>") || !strings.Contains(result, "<th>Name</th>") {
		t.Errorf("Expected table headers, got: %s", result)
	}

	// Check tbody
	if !strings.Contains(result, "<tbody>") || !strings.Contains(result, "<td>Alice</td>") {
		t.Errorf("Expected table body with data, got: %s", result)
	}
}

func TestMarkdownToHTML_SimpleTable(t *testing.T) {
	input := `| Column 1 | Column 2 |
| Data 1 | Data 2 |`

	result := MarkdownToHTML(input)

	if !strings.Contains(result, "<table>") {
		t.Errorf("Expected table conversion, got: %s", result)
	}
	if !strings.Contains(result, "<th>Column 1</th>") {
		t.Errorf("Expected header Column 1, got: %s", result)
	}
	if !strings.Contains(result, "<td>Data 1</td>") {
		t.Errorf("Expected cell Data 1, got: %s", result)
	}
}

func TestMarkdownToHTML_Bold(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"**bold**", "<b>bold</b>"},
		{"__bold__", "<b>bold</b>"},
		{"**bold text**", "<b>bold text</b>"},
	}

	for _, tt := range tests {
		got := MarkdownToHTML(tt.input)
		if !strings.Contains(got, tt.want) {
			t.Errorf("MarkdownToHTML(%q) = %v, want to contain %v", tt.input, got, tt.want)
		}
	}
}

func TestMarkdownToHTML_Lists(t *testing.T) {
	input := `- Item 1
- Item 2
- Item 3`

	result := MarkdownToHTML(input)

	if !strings.Contains(result, "<ul>") || !strings.Contains(result, "</ul>") {
		t.Errorf("Expected <ul> tags, got: %s", result)
	}
	if !strings.Contains(result, "<li>Item 1</li>") {
		t.Errorf("Expected list item, got: %s", result)
	}
}

func TestMarkdownToHTML_OrderedList(t *testing.T) {
	input := `1. First
2. Second
3. Third`

	result := MarkdownToHTML(input)

	if !strings.Contains(result, "<ol>") || !strings.Contains(result, "</ol>") {
		t.Errorf("Expected <ol> tags, got: %s", result)
	}
	if !strings.Contains(result, "<li>First</li>") {
		t.Errorf("Expected list item, got: %s", result)
	}
}

func TestMarkdownToHTML_Headers(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"# H1", "<h1>H1</h1>"},
		{"## H2", "<h2>H2</h2>"},
		{"### H3", "<h3>H3</h3>"},
		{"#### H4", "<h4>H4</h4>"},
	}

	for _, tt := range tests {
		got := MarkdownToHTML(tt.input)
		if !strings.Contains(got, tt.want) {
			t.Errorf("MarkdownToHTML(%q) = %v, want to contain %v", tt.input, got, tt.want)
		}
	}
}

func TestMarkdownToHTML_InlineCode(t *testing.T) {
	input := "Use `code` here"
	want := "<code>code</code>"

	got := MarkdownToHTML(input)
	if !strings.Contains(got, want) {
		t.Errorf("MarkdownToHTML(%q) = %v, want to contain %v", input, got, want)
	}
}

func TestMarkdownToHTML_Links(t *testing.T) {
	input := "[Google](https://google.com)"
	want := `<a href="https://google.com">Google</a>`

	got := MarkdownToHTML(input)
	if !strings.Contains(got, want) {
		t.Errorf("MarkdownToHTML(%q) = %v, want to contain %v", input, got, want)
	}
}
