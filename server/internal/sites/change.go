package sites

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Changes contains exactly one setting. The JSON tags are used by the CLI API.
// TODO: add way to change the name of the site too...
type Changes struct {
	Visibility *Visibility `json:"visibility"`
	Archived   *bool       `json:"archived"`
}

var ErrBadChange = errors.New("set either visibility to public or private, or archived to true or false")

// Change returns the record produced by this write, even if another request
// changes or deletes the Site before the caller sends its response.
func (s *Sites) Change(ctx context.Context, path string, change Changes) (Site, error) {
	if (change.Visibility == nil) == (change.Archived == nil) {
		return Site{}, ErrBadChange
	}
	field := "archived"
	var value any
	if change.Visibility != nil {
		if *change.Visibility != Public && *change.Visibility != Private {
			return Site{}, ErrBadChange
		}
		field = "visibility"
		value = *change.Visibility
	} else {
		value = *change.Archived
	}
	var site Site
	var updated int64
	err := s.db.QueryRowContext(ctx, `UPDATE sites SET `+field+` = ? WHERE path = ?
 RETURNING id,path,kind,file,folder,visibility,archived,updated_at`, value, path).
		Scan(&site.ID, &site.Path, &site.Kind, &site.File, &site.folder, &site.Visibility, &site.Archived, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	if err != nil {
		return Site{}, err
	}
	site.UpdatedAt = time.Unix(updated, 0)
	return site, nil
}
