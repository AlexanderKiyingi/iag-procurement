package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"sync"
)

//go:embed templates/*.html
var tmplFS embed.FS

var (
	tmplOnce sync.Once
	rootTmpl *template.Template
	tmplErr  error
)

func parseTemplates() {
	rootTmpl, tmplErr = template.ParseFS(tmplFS, "templates/*.html")
}

// RenderHTML executes the named template file (e.g. "alert.html") with data.
func RenderHTML(name string, data any) (string, error) {
	tmplOnce.Do(parseTemplates)
	if tmplErr != nil {
		return "", tmplErr
	}
	t := rootTmpl.Lookup(name)
	if t == nil {
		return "", fmt.Errorf("email: unknown template %q", name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// AlertData is used with templates/alert.html
type AlertData struct {
	Title, Message, Detail string
}
