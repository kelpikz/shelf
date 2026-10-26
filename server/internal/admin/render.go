package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

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
		"endsIn": endsIn,
	}
	pages := map[string]*template.Template{}
	for _, name := range []string{"login.html", "thanks.html", "sites.html", "cli.html"} {
		pages[name] = template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/base.html", "templates/"+name))
	}
	return pages
}

// endsIn rounds to the nearest day, the same as the CLI, so a new 30-day key
// shows 30, not 29.
func endsIn(now, end time.Time) string {
	const day = 24 * time.Hour
	left := end.Sub(now)
	if left < day {
		return "ends in less than one day"
	}
	if days := (left + day/2) / day; days != 1 {
		return fmt.Sprintf("ends in %d days", days)
	}
	return "ends in 1 day"
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
