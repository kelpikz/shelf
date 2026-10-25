package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/kelpikz/shelf/server/internal/auth"
)

var codeErrors = map[error]string{
	auth.ErrBadCode:      "The code is wrong or expired. Check the code in your terminal.",
	auth.ErrTooManyTries: "Too many wrong codes. Wait 10 minutes and try again.",
}

// cliApprovePage asks for the code that the CLI shows.
func (a *App) cliApprovePage(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.session(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, http.StatusOK, "cli.html", map[string]any{"Step": "code", "CSRF": sess.CSRF})
}

// cliApproveStep runs one step of the approval: "check" shows the login for
// a code, "approve" and "deny" decide it. GitHub login alone never approves.
func (a *App) cliApproveStep(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.checkPost(w, r); !ok {
		return
	}
	sess, _ := a.session(w, r)
	ctx, code := r.Context(), r.PostFormValue("code")
	page := map[string]any{"CSRF": sess.CSRF}

	var err error
	switch step := r.PathValue("step"); step {
	case "check":
		var login auth.PendingLogin
		if login, err = a.CLI.Find(ctx, code); err == nil {
			page["Step"], page["Login"] = "confirm", login
		}
	case "approve", "deny":
		if step == "approve" {
			err = a.CLI.Approve(ctx, code, r.PostFormValue("device_name"))
		} else {
			err = a.CLI.Deny(ctx, code)
		}
		if err == nil {
			page["Step"], page["Approved"] = "done", step == "approve"
		}
		// The login is still open; show it again with the error.
		if errors.Is(err, auth.ErrBadName) {
			var login auth.PendingLogin
			if login, err = a.CLI.Find(ctx, code); err == nil {
				page["Step"], page["Login"], page["Error"] = "confirm", login, auth.BadNameMessage
				a.render(w, http.StatusBadRequest, "cli.html", page)
				return
			}
		}
	default:
		http.NotFound(w, r)
		return
	}

	for e, msg := range codeErrors {
		if errors.Is(err, e) {
			page["Step"], page["Error"] = "code", msg
			a.render(w, http.StatusBadRequest, "cli.html", page)
			return
		}
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	a.render(w, http.StatusOK, "cli.html", page)
}

// renameKey sets the device name of one CLI key from the "CLI keys" list.
func (a *App) renameKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.checkPost(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, err = a.CLI.Rename(r.Context(), id, r.PostFormValue("device_name"))
	if errors.Is(err, auth.ErrBadName) {
		http.Redirect(w, r, "/?error=name", http.StatusSeeOther)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// revokeKey cancels one CLI key from the "CLI keys" list.
func (a *App) revokeKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.checkPost(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.CLI.Revoke(r.Context(), id); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
