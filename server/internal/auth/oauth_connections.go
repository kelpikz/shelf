package auth

import (
	"context"
	"time"
)

type OAuthConnection struct {
	ID, ClientID, Scope string
	ExpiresAt           time.Time
}

func (o *OAuth) Connections(ctx context.Context) ([]OAuthConnection, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT id,client_id,scope,expires_at FROM oauth_grants WHERE revoked=0 AND expires_at>? ORDER BY expires_at DESC LIMIT 100`, o.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OAuthConnection
	for rows.Next() {
		var c OAuthConnection
		var expires int64
		if err := rows.Scan(&c.ID, &c.ClientID, &c.Scope, &expires); err != nil {
			return nil, err
		}
		c.ExpiresAt = time.Unix(expires, 0)
		result = append(result, c)
	}
	return result, rows.Err()
}

// RevokeConnection requires an Owner browser session at the caller.
func (o *OAuth) RevokeConnection(ctx context.Context, id string) error {
	_, err := o.db.ExecContext(ctx, "UPDATE oauth_grants SET revoked=1 WHERE id=?", id)
	return err
}
