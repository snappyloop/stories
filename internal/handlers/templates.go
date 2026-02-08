package handlers

import (
	"embed"
	"html/template"
	"io"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// pageTemplates is the parsed set of all page templates (gtag, index, generation, agents, view_head, view_tail).
var pageTemplates = mustParseTemplates()

// viewTailBytes is cached output of view_tail (no dynamic data).
// view_head is rendered per-request with job type for theme.
var viewTailBytes []byte

func mustParseTemplates() *template.Template {
	t, err := template.New("").ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		panic("parse templates: " + err.Error())
	}
	return t
}

func init() {
	var err error
	viewTailBytes, err = executeTemplateToBytes("view_tail", nil)
	if err != nil {
		panic("view_tail: " + err.Error())
	}
}

// executeTemplate executes the named template (e.g. "index", "generation", "agents") with data into w.
func executeTemplate(w io.Writer, name string, data interface{}) error {
	return pageTemplates.ExecuteTemplate(w, name, data)
}

// executeTemplateToBytes runs executeTemplate into a buffer and returns the bytes.
func executeTemplateToBytes(name string, data interface{}) ([]byte, error) {
	var b []byte
	buf := &byteBuffer{b: &b}
	err := pageTemplates.ExecuteTemplate(buf, name, data)
	return b, err
}

// byteBuffer wraps a byte slice so it can be used as io.Writer for template execution.
type byteBuffer struct {
	b *[]byte
}

func (b *byteBuffer) Write(p []byte) (n int, err error) {
	*b.b = append(*b.b, p...)
	return len(p), nil
}
