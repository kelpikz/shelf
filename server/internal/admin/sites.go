package admin

import (
	"errors"
	"log"
	"net/http"

	"github.com/kelpikz/shelf/server/internal/auth"
	"github.com/kelpikz/shelf/server/internal/sites"
)

var siteErrors = map[string]string{
	"confirm": "Nothing was deleted. Type the path exactly to delete a Site.",
	"gone":    "That Site does not exist any more.",
	"name":    "The name did not change. " + auth.BadNameMessage,
}

// siteAction runs one button of the Admin page on one Site.
func (a *App) siteAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.checkPost(w, r); !ok {
		return
	}
	ctx, path := r.Context(), r.PathValue("path")
	var err error
	switch r.PathValue("action") {
	case "public":
		err = a.Sites.SetVisibility(ctx, path, sites.Public)
	case "private":
		err = a.Sites.SetVisibility(ctx, path, sites.Private)
	case "archive":
		err = a.Sites.SetArchived(ctx, path, true)
	case "unarchive":
		err = a.Sites.SetArchived(ctx, path, false)
	case "delete":
		// The Owner types the path to confirm.
		if r.PostFormValue("confirm") != path {
			http.Redirect(w, r, "/?error=confirm", http.StatusSeeOther)
			return
		}
		err = a.Sites.Delete(ctx, path)
		if err == nil {
			log.Printf("sites: deleted %s", path)
		}
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, sites.ErrNotFound) {
		http.Redirect(w, r, "/?error=gone", http.StatusSeeOther)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
