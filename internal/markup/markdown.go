package markup

import (
	"regexp"
	"strings"
)

// MarkdownToHTML converts markdown to basic HTML tags.
// Supported: at-line-start headers (#–####), code blocks, inline code, links, bold (** or __), italic (* or _), strikethrough (~~).
// Escapes HTML in non-tag content to prevent injection.
func MarkdownToHTML(text string) string {
	if text == "" {
		return text
	}
	// Escape all HTML upfront to prevent XSS, then process markdown patterns.
	// Pattern handlers do NOT re-escape since input is already safe.
	return processMarkdown(escapeHTMLForMarkdown(text))
}

// processMarkdown converts markdown syntax to HTML tags.
// IMPORTANT: Input MUST already be HTML-escaped. This function does not escape.
func processMarkdown(text string) string {
	result := text

	// Horizontal rules: --- or *** or ___ on their own line
	hrRegex := regexp.MustCompile(`(?m)^(?:---+|\*\*\*+|___+)\s*$`)
	result = hrRegex.ReplaceAllString(result, "<hr>")

	// At-line-start headers: #### → h4, ### → h3, ## → h2, # → h1 (process longest first)
	for _, pair := range []struct {
		re  *regexp.Regexp
		tag string
	}{
		{regexp.MustCompile(`(?m)^#### (.+)$`), "h4"},
		{regexp.MustCompile(`(?m)^### (.+)$`), "h3"},
		{regexp.MustCompile(`(?m)^## (.+)$`), "h2"},
		{regexp.MustCompile(`(?m)^# (.+)$`), "h1"},
	} {
		result = pair.re.ReplaceAllStringFunc(result, func(match string) string {
			sub := pair.re.FindStringSubmatch(match)
			if len(sub) < 2 {
				return match
			}
			return "<" + pair.tag + ">" + strings.TrimSpace(sub[1]) + "</" + pair.tag + ">"
		})
	}

	// Tables: | col1 | col2 | format (before lists to avoid conflicts)
	result = convertMarkdownTables(result)

	// Unordered and ordered lists (line-based; process after headers and tables)
	result = convertMarkdownLists(result)

	// Process in order: code blocks -> inline code -> links -> bold -> italic -> strikethrough
	// NOTE: Input is already HTML-escaped, so we do NOT call escapeHTMLForMarkdown here.
	codeBlockRegex := regexp.MustCompile("(?s)```([^`]+)```")
	result = codeBlockRegex.ReplaceAllStringFunc(result, func(match string) string {
		code := codeBlockRegex.FindStringSubmatch(match)[1]
		code = strings.Trim(code, "\n\r")
		return "<pre>" + code + "</pre>"
	})

	inlineCodeRegex := regexp.MustCompile("`([^`\n]+)`")
	result = inlineCodeRegex.ReplaceAllStringFunc(result, func(match string) string {
		code := inlineCodeRegex.FindStringSubmatch(match)[1]
		return "<code>" + code + "</code>"
	})

	linkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	result = linkRegex.ReplaceAllStringFunc(result, func(match string) string {
		matches := linkRegex.FindStringSubmatch(match)
		linkText := matches[1]
		url := matches[2]
		return "<a href=\"" + url + "\">" + linkText + "</a>"
	})

	boldDoubleAsteriskRegex := regexp.MustCompile(`\*\*([^*]+)\*\*`)
	result = boldDoubleAsteriskRegex.ReplaceAllStringFunc(result, func(match string) string {
		content := boldDoubleAsteriskRegex.FindStringSubmatch(match)[1]
		return "<b>" + content + "</b>"
	})

	boldDoubleUnderscoreRegex := regexp.MustCompile(`__([^_]+)__`)
	result = boldDoubleUnderscoreRegex.ReplaceAllStringFunc(result, func(match string) string {
		content := boldDoubleUnderscoreRegex.FindStringSubmatch(match)[1]
		return "<b>" + content + "</b>"
	})

	// Italic: single * (avoid emoji asterisks)
	var newResult strings.Builder
	runes := []rune(result)
	i := 0
	for i < len(runes) {
		if runes[i] == '*' && i+1 < len(runes) {
			nextRune := runes[i+1]
			if nextRune == 0xFE0F || nextRune == 0x20E3 {
				newResult.WriteRune(runes[i])
				i++
				continue
			}
			found := false
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '\n' {
					break
				}
				if runes[j] == '*' {
					content := string(runes[i+1 : j])
					if strings.TrimSpace(content) == "" {
						break
					}
					hasLetterOrNumber := false
					for _, r := range content {
						if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
							(r >= 0x0400 && r <= 0x04FF) || (r >= 0x0100 && r <= 0x017F) || (r >= 0x0180 && r <= 0x024F) {
							hasLetterOrNumber = true
							break
						}
					}
					if hasLetterOrNumber {
						newResult.WriteString("<i>")
						newResult.WriteString(content)
						newResult.WriteString("</i>")
						i = j + 1
						found = true
						break
					}
				}
			}
			if !found {
				newResult.WriteRune(runes[i])
				i++
			}
		} else {
			newResult.WriteRune(runes[i])
			i++
		}
	}
	result = newResult.String()

	italicUnderscoreRegex := regexp.MustCompile(`_([^_\n]+?)_`)
	result = italicUnderscoreRegex.ReplaceAllStringFunc(result, func(match string) string {
		content := italicUnderscoreRegex.FindStringSubmatch(match)[1]
		if strings.TrimSpace(content) == "" {
			return match
		}
		hasLetterOrNumber := false
		for _, r := range content {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				(r >= 0x0400 && r <= 0x04FF) || (r >= 0x0100 && r <= 0x017F) || (r >= 0x0180 && r <= 0x024F) {
				hasLetterOrNumber = true
				break
			}
		}
		if !hasLetterOrNumber {
			return match
		}
		return "<i>" + content + "</i>"
	})

	strikethroughRegex := regexp.MustCompile(`~~([^~]+)~~`)
	result = strikethroughRegex.ReplaceAllStringFunc(result, func(match string) string {
		content := strikethroughRegex.FindStringSubmatch(match)[1]
		return "<s>" + content + "</s>"
	})

	return result
}

// convertMarkdownLists turns consecutive list lines into <ul>/<ol> and <li>, supporting nested lists.
// Unordered: lines starting with - , * , or + (optionally indented).
// Ordered: lines starting with 1. , 2. , etc. (optionally indented).
// IMPORTANT: Input MUST already be HTML-escaped.
func convertMarkdownLists(text string) string {
	lines := strings.Split(text, "\n")
	var out strings.Builder
	i := 0
	for i < len(lines) {
		line := lines[i]
		// Check if this line starts a list (with optional leading whitespace)
		indent, listType, _ := parseListLine(line)
		if listType != "" {
			// Consume all consecutive list lines (including nested)
			consumed, html := consumeList(lines, i, indent)
			out.WriteString(html)
			i += consumed
			continue
		}
		// Plain line
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
		i++
	}
	return out.String()
}

// parseListLine returns (indent, listType, content) where listType is "ul", "ol", or "".
// indent is the number of leading spaces/tabs (converted to spaces, tab=4).
func parseListLine(line string) (int, string, string) {
	indent := 0
	for _, r := range line {
		if r == ' ' {
			indent++
		} else if r == '\t' {
			indent += 4
		} else {
			break
		}
	}
	trimmed := strings.TrimLeft(line, " \t")
	// Unordered: - , * , or +
	ulRegex := regexp.MustCompile(`^[-*+]\s+(.*)$`)
	if m := ulRegex.FindStringSubmatch(trimmed); m != nil {
		return indent, "ul", m[1]
	}
	// Ordered: number.
	olRegex := regexp.MustCompile(`^(\d+)\.\s+(.*)$`)
	if m := olRegex.FindStringSubmatch(trimmed); m != nil {
		return indent, "ol", m[2]
	}
	return indent, "", ""
}

// consumeList consumes lines[start:] that belong to the same list (and nested lists), returns (count, html).
func consumeList(lines []string, start int, baseIndent int) (int, string) {
	startIndent, listType, _ := parseListLine(lines[start])
	if listType == "" {
		return 0, ""
	}
	var items []string
	i := start
	for i < len(lines) {
		indent, lt, content := parseListLine(lines[i])
		if lt == "" {
			// Not a list line; check if it's a blank line
			if strings.TrimSpace(lines[i]) == "" {
				// Blank line; peek ahead to see if the list continues
				j := i + 1
				for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
					j++ // skip consecutive blank lines
				}
				if j < len(lines) {
					peekIndent, peekType, _ := parseListLine(lines[j])
					if peekIndent == startIndent && peekType == listType {
						// List continues after blank line(s); skip blanks and continue
						i = j
						continue
					}
				}
			}
			// Non-blank non-list line; end of list
			break
		}
		if indent < startIndent {
			// Less indented; end of this list
			break
		}
		if indent == startIndent && lt == listType {
			// Same-level item; collect content and any nested children
			itemContent := content
			i++
			// Check for nested list lines (more indented)
			var nestedLines []string
			for i < len(lines) {
				nIndent, nLT, _ := parseListLine(lines[i])
				if nLT == "" {
					// Not a list line; could be continuation text if indented, or blank
					if strings.TrimSpace(lines[i]) == "" {
						// Blank line; check if nested content continues or main list continues
						j := i + 1
						for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
							j++
						}
						if j < len(lines) {
							pIndent, pType, _ := parseListLine(lines[j])
							if pType != "" && pIndent > startIndent {
								// Nested list continues after blank
								nestedLines = append(nestedLines, lines[i])
								i++
								continue
							}
						}
						// Not nested continuation; exit nested loop
						break
					} else if countLeadingSpaces(lines[i]) > startIndent {
						// Continuation line (indented plain text)
						nestedLines = append(nestedLines, lines[i])
						i++
					} else {
						break
					}
				} else if nIndent > startIndent {
					// Nested list
					nestedLines = append(nestedLines, lines[i])
					i++
				} else {
					// Same or less indent; not nested
					break
				}
			}
			if len(nestedLines) > 0 {
				// Process nested content
				nestedHTML := convertMarkdownLists(strings.Join(nestedLines, "\n"))
				itemContent += "\n" + nestedHTML
			}
			items = append(items, itemContent)
		} else {
			// Different list type or indent mismatch; end
			break
		}
	}
	tag := listType
	var html strings.Builder
	html.WriteString("<")
	html.WriteString(tag)
	html.WriteString(">")
	for _, item := range items {
		html.WriteString("<li>")
		html.WriteString(processInlineMarkdown(item))
		html.WriteString("</li>")
	}
	html.WriteString("</")
	html.WriteString(tag)
	html.WriteString(">\n")
	return i - start, html.String()
}

func countLeadingSpaces(line string) int {
	count := 0
	for _, r := range line {
		if r == ' ' {
			count++
		} else if r == '\t' {
			count += 4
		} else {
			break
		}
	}
	return count
}

// processInlineMarkdown processes inline markdown (bold, italic, code, links) without lists/headers.
func processInlineMarkdown(text string) string {
	result := strings.TrimSpace(text)
	// Inline code
	inlineCodeRegex := regexp.MustCompile("`([^`\n]+)`")
	result = inlineCodeRegex.ReplaceAllStringFunc(result, func(match string) string {
		code := inlineCodeRegex.FindStringSubmatch(match)[1]
		return "<code>" + code + "</code>"
	})
	// Links
	linkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	result = linkRegex.ReplaceAllStringFunc(result, func(match string) string {
		matches := linkRegex.FindStringSubmatch(match)
		return "<a href=\"" + matches[2] + "\">" + matches[1] + "</a>"
	})
	// Bold
	boldRegex := regexp.MustCompile(`\*\*([^*]+)\*\*`)
	result = boldRegex.ReplaceAllStringFunc(result, func(match string) string {
		return "<b>" + boldRegex.FindStringSubmatch(match)[1] + "</b>"
	})
	boldRegex2 := regexp.MustCompile(`__([^_]+)__`)
	result = boldRegex2.ReplaceAllStringFunc(result, func(match string) string {
		return "<b>" + boldRegex2.FindStringSubmatch(match)[1] + "</b>"
	})
	// Italic (single *)
	italicRegex := regexp.MustCompile(`\*([^*\n]+)\*`)
	result = italicRegex.ReplaceAllStringFunc(result, func(match string) string {
		content := italicRegex.FindStringSubmatch(match)[1]
		if strings.TrimSpace(content) == "" {
			return match
		}
		return "<i>" + content + "</i>"
	})
	// Italic (single _)
	italicRegex2 := regexp.MustCompile(`_([^_\n]+)_`)
	result = italicRegex2.ReplaceAllStringFunc(result, func(match string) string {
		content := italicRegex2.FindStringSubmatch(match)[1]
		if strings.TrimSpace(content) == "" {
			return match
		}
		return "<i>" + content + "</i>"
	})
	// Strikethrough
	strikeRegex := regexp.MustCompile(`~~([^~]+)~~`)
	result = strikeRegex.ReplaceAllStringFunc(result, func(match string) string {
		return "<s>" + strikeRegex.FindStringSubmatch(match)[1] + "</s>"
	})
	return result
}

// convertMarkdownTables converts markdown tables (| col1 | col2 |) to HTML tables.
func convertMarkdownTables(text string) string {
	lines := strings.Split(text, "\n")
	var out strings.Builder
	i := 0
	for i < len(lines) {
		line := lines[i]
		// Check if line looks like a table row (starts with |)
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			// Collect consecutive table lines
			var tableLines []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				tableLines = append(tableLines, lines[i])
				i++
			}
			if len(tableLines) > 0 {
				out.WriteString(renderTable(tableLines))
				continue
			}
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
		i++
	}
	return out.String()
}

func renderTable(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var html strings.Builder
	html.WriteString("<table>")
	// First line is header
	headerCells := parseTableRow(lines[0])
	if len(headerCells) > 0 {
		html.WriteString("<thead><tr>")
		for _, cell := range headerCells {
			html.WriteString("<th>")
			html.WriteString(processInlineMarkdown(cell))
			html.WriteString("</th>")
		}
		html.WriteString("</tr></thead>")
	}
	// Skip separator line if present (e.g., |---|---|)
	start := 1
	if len(lines) > 1 && isSeparatorLine(lines[1]) {
		start = 2
	}
	// Body rows
	if start < len(lines) {
		html.WriteString("<tbody>")
		for i := start; i < len(lines); i++ {
			cells := parseTableRow(lines[i])
			if len(cells) > 0 {
				html.WriteString("<tr>")
				for _, cell := range cells {
					html.WriteString("<td>")
					html.WriteString(processInlineMarkdown(cell))
					html.WriteString("</td>")
				}
				html.WriteString("</tr>")
			}
		}
		html.WriteString("</tbody>")
	}
	html.WriteString("</table>\n")
	return html.String()
}

func parseTableRow(line string) []string {
	// Split by | and trim
	parts := strings.Split(line, "|")
	var cells []string
	for i, p := range parts {
		trimmed := strings.TrimSpace(p)
		// Skip first/last if empty (leading/trailing |)
		if i == 0 || i == len(parts)-1 {
			if trimmed == "" {
				continue
			}
		}
		cells = append(cells, trimmed)
	}
	return cells
}

func isSeparatorLine(line string) bool {
	// Check if line is like |---|---| or | --- | --- |
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	// Remove all |, -, and whitespace; if nothing left, it's a separator
	cleaned := strings.ReplaceAll(trimmed, "|", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, ":", "")
	return cleaned == ""
}

func escapeHTMLForMarkdown(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")
	return text
}
