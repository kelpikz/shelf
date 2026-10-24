package admin

import (
	"bytes"
	"embed"
	"html/template"
	"log"
	"net/http"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/sites"
)

//go:embed templates static
var files embed.FS

func loadPages() map[string]*template.Template {
	funcs := template.FuncMap{
		// site gives the "site" template one Site and what its forms need.
		"site": func(page map[string]any, site sites.Site) map[string]any {
			return map[string]any{"Site": site, "FilesURL": page["FilesURL"], "CSRF": page["Session"].(auth.Session).CSRF}
		},
	}
	pages := map[string]*template.Template{}
	for _, name := range []string{"login.html", "thanks.html", "sites.html"} {
		pages[name] = template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/base.html", "templates/"+name))
	}
	return pages
}

func (a *App) render(w http.ResponseWriter, status int, page string, data any) {
	var buf bytes.Buffer
	if err := a.pages[page].ExecuteTemplate(&buf, "base", data); err != nil {
		a.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (a *App) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}
