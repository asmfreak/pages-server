package templates

//go:generate go run ./templates/fetchJS

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"html/template"
	"sync"

	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/html"
)

func compileTemplate(name, data string) (*template.Template, error) {
	m := minify.New()
	m.Add("text/html", &html.Minifier{
		KeepDocumentTags: true,
		KeepEndTags:      true,
	})
	tmpl := template.New(name).Funcs(template.FuncMap{
		"IndexJSFileName": IndexJSFileName,
	})
	ms, err := m.String("text/html", data)
	if err != nil {
		return nil, err
	}
	return tmpl.Parse(ms)
}

//go:embed index.js
var IndexJS []byte

var IndexJSFileName = sync.OnceValue(func() string {
	return fmt.Sprintf("/__%x_index.js", sha256.Sum256(IndexJS))
})

//go:embed index.html
var index string
var Index = template.Must(compileTemplate("index", index))

//go:embed login.html
var login string
var Login = template.Must(compileTemplate("login", login))

//go:embed preparation.html
var preparation string
var Preparation = template.Must(compileTemplate("preparation", preparation))

//go:embed error.html
var errorPageText string
var Error = template.Must(compileTemplate("error", errorPageText))
