package markup

import (
	"html"
	"regexp"
	"strings"
)

// ToHTML converts job output markup to basic HTML.
// Markup format: [[SOURCE file_id=... filename="..."]], [[SEGMENT id=...]], [[IMAGE asset_id=...]], [[AUDIO asset_id=...]].
// jobID is used to build asset URLs: /view/asset/{id}?job_id={jobID}
func ToHTML(markup, jobID string) string {
	if markup == "" {
		return ""
	}
	var out strings.Builder
	jobID = html.EscapeString(jobID)

	// Skip SOURCE blocks (excluded from view output)
	sourceRe := regexp.MustCompile(`(?s)\[\[SOURCE file_id=[^ \]]+\s+filename="[^"]*"\]\](.*?)\[\[/SOURCE\]\]`)
	idx := 0
	for _, m := range sourceRe.FindAllStringSubmatchIndex(markup, -1) {
		out.WriteString(html.EscapeString(markup[idx:m[0]]))
		idx = m[1]
	}

	// [[SEGMENT id=uuid]] ... [[/SEGMENT]]
	segRe := regexp.MustCompile(`(?s)\[\[SEGMENT id=([^ \]]+)\]\](.*?)\[\[/SEGMENT\]\]`)
	for _, m := range segRe.FindAllStringSubmatchIndex(markup, -1) {
		if m[0] > idx {
			out.WriteString(html.EscapeString(markup[idx:m[0]]))
		}
		segID := html.EscapeString(markup[m[2]:m[3]])
		inner := markup[m[4]:m[5]]
		out.WriteString(`<div class="segment" data-segment-id="`)
		out.WriteString(segID)
		out.WriteString(`">`)
		out.WriteString(segmentInnerToHTML(inner, jobID))
		out.WriteString(`</div>`)
		idx = m[1]
	}

	// any remaining markup (e.g. plain text between blocks)
	if idx < len(markup) {
		out.WriteString(html.EscapeString(markup[idx:]))
	}

	return out.String()
}

func segmentInnerToHTML(inner, jobID string) string {
	audioRe := regexp.MustCompile(`\[\[AUDIO asset_id=([a-fA-F0-9-]+)\]\]`)
	imageRe := regexp.MustCompile(`\[\[IMAGE asset_id=([a-fA-F0-9-]+)\]\]`)

	// Collect audio IDs, image IDs, and strip both to get segment text only
	var audioIDs, imageIDs []string
	textOnly := audioRe.ReplaceAllString(inner, "")
	textOnly = imageRe.ReplaceAllString(textOnly, "")
	// Collect in order (audios first, then images) for deterministic output
	for _, sub := range audioRe.FindAllStringSubmatch(inner, -1) {
		if len(sub) >= 2 {
			audioIDs = append(audioIDs, sub[1])
		}
	}
	for _, sub := range imageRe.FindAllStringSubmatch(inner, -1) {
		if len(sub) >= 2 {
			imageIDs = append(imageIDs, sub[1])
		}
	}

	var b strings.Builder
	// 1. Audio before segment
	for _, id := range audioIDs {
		id = html.EscapeString(id)
		b.WriteString(`<audio controls preload="metadata" src="/view/asset/`)
		b.WriteString(id)
		b.WriteString(`?job_id=`)
		b.WriteString(jobID)
		b.WriteString(`"></audio>`)
	}
	// 2. Segment text (title + body)
	emitSegmentText(&b, textOnly)
	// 3. Image after segment
	for _, id := range imageIDs {
		id = html.EscapeString(id)
		b.WriteString(`<img class="segment-image" src="/view/asset/`)
		b.WriteString(id)
		b.WriteString(`?job_id=`)
		b.WriteString(jobID)
		b.WriteString(`" alt="">`)
	}
	return b.String()
}

// emitSegmentText outputs segment body: optional # Title line, then paragraph(s).
// Text is converted from markdown to HTML (bold, italic, links, code, etc.) for the view page.
func emitSegmentText(b *strings.Builder, s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	lines := strings.Split(s, "\n")
	var i int
	// optional first line as # Title
	if len(lines) > 0 && strings.HasPrefix(lines[0], "# ") {
		title := strings.TrimSpace(lines[0][2:])
		b.WriteString("<h2 class=\"segment-title\">")
		b.WriteString(MarkdownToHTML(title))
		b.WriteString("</h2>")
		i = 1
	}
	// rest as one paragraph with preserved newlines; markdown converted to HTML
	if i < len(lines) {
		body := strings.TrimSpace(strings.Join(lines[i:], "\n"))
		if body != "" {
			b.WriteString("<p class=\"segment-text\">")
			b.WriteString(MarkdownToHTML(body))
			b.WriteString("</p>")
		}
	}
}
