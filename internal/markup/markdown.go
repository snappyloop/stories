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

	// Unordered and ordered lists (line-based; process after headers)
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

// convertMarkdownLists turns consecutive list lines into <ul>/<ol> and <li>.
// Unordered: lines starting with - , * , or + .
// Ordered: lines starting with 1. , 2. , etc.
// IMPORTANT: Input MUST already be HTML-escaped. Recursive calls use processMarkdown (not MarkdownToHTML)
// to avoid double-escaping.
func convertMarkdownLists(text string) string {
	lines := strings.Split(text, "\n")
	ulPrefix := regexp.MustCompile(`^[-*+] (.+)$`)
	olPrefix := regexp.MustCompile(`^(\d+)\. (.+)$`)

	var out strings.Builder
	i := 0
	for i < len(lines) {
		line := lines[i]
		if ulPrefix.FindStringSubmatch(line) != nil {
			var items []string
			for i < len(lines) {
				if sub := ulPrefix.FindStringSubmatch(lines[i]); sub != nil {
					// Use processMarkdown (not MarkdownToHTML) to avoid double-escaping
					items = append(items, processMarkdown(sub[1]))
					i++
				} else {
					break
				}
			}
			out.WriteString("<ul>")
			for _, item := range items {
				out.WriteString("<li>")
				out.WriteString(item)
				out.WriteString("</li>")
			}
			out.WriteString("</ul>\n")
			continue
		}
		if olPrefix.FindStringSubmatch(line) != nil {
			var items []string
			for i < len(lines) {
				if sub := olPrefix.FindStringSubmatch(lines[i]); sub != nil {
					// Use processMarkdown (not MarkdownToHTML) to avoid double-escaping
					items = append(items, processMarkdown(sub[2]))
					i++
				} else {
					break
				}
			}
			out.WriteString("<ol>")
			for _, item := range items {
				out.WriteString("<li>")
				out.WriteString(item)
				out.WriteString("</li>")
			}
			out.WriteString("</ol>\n")
			continue
		}
		// Plain line - already escaped by MarkdownToHTML, write as-is
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
		i++
	}
	return out.String()
}

func escapeHTMLForMarkdown(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")
	return text
}
