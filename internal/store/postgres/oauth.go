package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) CreateOAuthClient(ctx context.Context, c store.OAuthClient) error {
	var secret *string
	if c.SecretSHA256 != "" {
		secret = &c.SecretSHA256
	}
	_, err := s.db.ExecContext(ctx,
		q(`INSERT INTO oauth_clients (id, kind, name, redirect_uris, secret_sha256, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`),
		c.ID, c.Kind, c.Name, strings.Join(c.RedirectURIs, "\n"), secret, c.CreatedAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

const oauthClientColumns = `id, kind, name, redirect_uris, secret_sha256, created_at`

func scanOAuthClient(row interface{ Scan(...any) error }) (store.OAuthClient, error) {
	var c store.OAuthClient
	var uris string
	var secret sql.NullString
	if err := row.Scan(&c.ID, &c.Kind, &c.Name, &uris, &secret, &c.CreatedAt); err != nil {
		return c, err
	}
	c.RedirectURIs = splitLines(uris)
	c.SecretSHA256 = secret.String
	return c, nil
}

func (s *Store) OAuthClient(ctx context.Context, id string) (store.OAuthClient, error) {
	c, err := scanOAuthClient(s.db.QueryRowContext(ctx,
		q(`SELECT `+oauthClientColumns+` FROM oauth_clients WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, store.ErrNotFound
	}
	return c, err
}

func (s *Store) ListOAuthClients(ctx context.Context) ([]store.OAuthClient, error) {
	rows, err := s.db.QueryContext(ctx,
		q(`SELECT `+oauthClientColumns+` FROM oauth_clients ORDER BY created_at, id`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.OAuthClient
	for rows.Next() {
		c, err := scanOAuthClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteOAuthClient(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, q(`DELETE FROM oauth_clients WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CreateOAuthCode(ctx context.Context, c store.OAuthCode) error {
	_, err := s.db.ExecContext(ctx,
		q(`INSERT INTO oauth_codes (sha256, user_id, client_id, client_name, redirect_uri,
		   code_challenge, resource, scopes, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		c.SHA256, c.UserID, c.ClientID, c.ClientName, c.RedirectURI, c.CodeChallenge,
		c.Resource, strings.Join(c.Scopes, " "), c.CreatedAt.UTC(), c.ExpiresAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) ExchangeOAuthCode(ctx context.Context, r store.OAuthCodeRedemption, now time.Time) (store.OAuthGrant, error) {
	var g store.OAuthGrant
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return g, err
	}
	defer tx.Rollback()
	var userID, clientID, clientName, redirectURI, challenge, resource, scopes string
	var expires time.Time
	var used *time.Time
	var codeGrant sql.NullString
	err = tx.QueryRowContext(ctx,
		q(`SELECT user_id, client_id, client_name, redirect_uri, code_challenge, resource,
		        scopes, expires_at, used_at, grant_id
		   FROM oauth_codes WHERE sha256 = ?`), r.SHA256).
		Scan(&userID, &clientID, &clientName, &redirectURI, &challenge, &resource,
			&scopes, &expires, &used, &codeGrant)
	if errors.Is(err, sql.ErrNoRows) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if err != nil {
		return store.OAuthGrant{}, err
	}
	if clientID != r.ClientID || (r.RedirectURI != "" && redirectURI != r.RedirectURI) || challenge != r.CodeChallenge ||
		(r.Resource != "" && r.Resource != resource) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if used != nil {
		return store.OAuthGrant{}, revokeSpentCode(ctx, tx, r.SHA256, now)
	}
	if !now.Before(expires) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	// A concurrent use that got there first leaves nothing to update,
	// and this one loses exactly as a later replay would.
	res, err := tx.ExecContext(ctx,
		q(`UPDATE oauth_codes SET used_at = ? WHERE sha256 = ? AND used_at IS NULL`), now.UTC(), r.SHA256)
	if err != nil {
		return store.OAuthGrant{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return store.OAuthGrant{}, err
	} else if n != 1 {
		return store.OAuthGrant{}, revokeSpentCode(ctx, tx, r.SHA256, now)
	}
	g = store.OAuthGrant{
		ID: r.GrantID, UserID: userID, ClientID: clientID, ClientName: clientName,
		Scopes: strings.Fields(scopes), Resource: resource, CreatedAt: now.UTC(),
	}
	if _, err := tx.ExecContext(ctx,
		q(`INSERT INTO oauth_grants (id, user_id, client_id, client_name, scopes, resource, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`),
		g.ID, g.UserID, g.ClientID, g.ClientName, strings.Join(g.Scopes, " "), g.Resource,
		g.CreatedAt); err != nil {
		if isUniqueErr(err) {
			return store.OAuthGrant{}, store.ErrConflict
		}
		return store.OAuthGrant{}, err
	}
	if _, err := tx.ExecContext(ctx,
		q(`UPDATE oauth_codes SET grant_id = ? WHERE sha256 = ?`), g.ID, r.SHA256); err != nil {
		return store.OAuthGrant{}, err
	}
	if err := insertOAuthTokens(ctx, tx, g.ID, r.Tokens); err != nil {
		return store.OAuthGrant{}, err
	}
	return g, tx.Commit()
}

// revokeSpentCode revokes whatever grant a spent code produced, in the
// caller's transaction, and answers the caller with ErrConflict. It
// reads grant_id again rather than trusting an earlier look: the
// redemption that won the race writes used_at and grant_id in one
// transaction, so a redemption that lost the conditional update must
// read the winner's grant_id to cut it. A code spent without a grant
// (revoking every credential spends them) leaves nothing to revoke.
func revokeSpentCode(ctx context.Context, tx *sql.Tx, sha256 string, now time.Time) error {
	var grant sql.NullString
	err := tx.QueryRowContext(ctx, q(`SELECT grant_id FROM oauth_codes WHERE sha256 = ?`), sha256).Scan(&grant)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	if grant.Valid {
		if _, err := tx.ExecContext(ctx,
			q(`UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`),
			now.UTC(), grant.String); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return store.ErrConflict
}

func insertOAuthTokens(ctx context.Context, tx *sql.Tx, grantID string, tokens []store.OAuthToken) error {
	for _, t := range tokens {
		if _, err := tx.ExecContext(ctx,
			q(`INSERT INTO oauth_tokens (sha256, grant_id, kind, created_at, expires_at)
			 VALUES (?, ?, ?, ?, ?)`),
			t.SHA256, grantID, t.Kind, t.CreatedAt.UTC(), t.ExpiresAt.UTC()); err != nil {
			if isUniqueErr(err) {
				return store.ErrConflict
			}
			return err
		}
	}
	return nil
}

func (s *Store) OAuthAccess(ctx context.Context, sha256 string, now time.Time) (store.OAuthAccess, error) {
	var a store.OAuthAccess
	var scopes string
	err := s.db.QueryRowContext(ctx,
		q(`SELECT g.id, g.user_id, g.client_id, g.scopes, g.resource, t.expires_at
		   FROM oauth_tokens t
		   JOIN oauth_grants g ON g.id = t.grant_id
		   JOIN users u ON u.id = g.user_id
		  WHERE t.sha256 = ? AND t.kind = 'access' AND t.expires_at > ?
		    AND g.revoked_at IS NULL AND u.disabled_at IS NULL`),
		sha256, now.UTC()).
		Scan(&a.GrantID, &a.UserID, &a.ClientID, &scopes, &a.Resource, &a.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, store.ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.Scopes = strings.Fields(scopes)
	return a, nil
}

func (s *Store) RotateOAuthRefresh(
	ctx context.Context, sha256, clientID string, now time.Time, tokens []store.OAuthToken,
) (store.OAuthGrant, error) {
	var g store.OAuthGrant
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return g, err
	}
	defer tx.Rollback()
	nowS := now.UTC()
	var used, revoked, disabled *time.Time
	var expires time.Time
	var scopes string
	err = tx.QueryRowContext(ctx,
		q(`SELECT t.used_at, t.expires_at, g.id, g.user_id, g.client_id, g.client_name,
		        g.scopes, g.resource, g.created_at, g.revoked_at, u.disabled_at
		   FROM oauth_tokens t
		   JOIN oauth_grants g ON g.id = t.grant_id
		   JOIN users u ON u.id = g.user_id
		  WHERE t.sha256 = ? AND t.kind = 'refresh' FOR UPDATE OF g`), sha256).
		Scan(&used, &expires, &g.ID, &g.UserID, &g.ClientID, &g.ClientName,
			&scopes, &g.Resource, &g.CreatedAt, &revoked, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if err != nil {
		return g, err
	}
	if g.ClientID != clientID {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if used != nil {
		if _, err := tx.ExecContext(ctx,
			q(`UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`),
			nowS, g.ID); err != nil {
			return g, err
		}
		if err := tx.Commit(); err != nil {
			return g, err
		}
		return store.OAuthGrant{}, store.ErrConflict
	}
	if revoked != nil || disabled != nil || !nowS.Before(expires) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	// A concurrent use that got there first leaves nothing to update,
	// and this one loses exactly as a later replay would.
	res, err := tx.ExecContext(ctx,
		q(`UPDATE oauth_tokens SET used_at = ? WHERE sha256 = ? AND used_at IS NULL`), nowS, sha256)
	if err != nil {
		return g, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return g, err
	} else if n != 1 {
		// The request that won the race has already stored fresh tokens
		// for this grant. Reuse means the refresh token leaked, so those
		// tokens go with the grant, as they do for a later replay.
		if _, err := tx.ExecContext(ctx,
			q(`UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`), nowS, g.ID); err != nil {
			return g, err
		}
		if err := tx.Commit(); err != nil {
			return g, err
		}
		return store.OAuthGrant{}, store.ErrConflict
	}
	// The access token issued with the spent refresh token goes too, so
	// a grant never has more than one live access token.
	if _, err := tx.ExecContext(ctx,
		q(`DELETE FROM oauth_tokens WHERE grant_id = ? AND kind = 'access'`), g.ID); err != nil {
		return g, err
	}
	if err := insertOAuthTokens(ctx, tx, g.ID, tokens); err != nil {
		return g, err
	}
	if _, err := tx.ExecContext(ctx,
		q(`UPDATE oauth_grants SET last_used_at = ? WHERE id = ?`), nowS, g.ID); err != nil {
		return g, err
	}
	g.Scopes = strings.Fields(scopes)
	g.LastUsedAt = &now
	return g, tx.Commit()
}

func (s *Store) TouchOAuthGrant(ctx context.Context, grantID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		q(`UPDATE oauth_grants SET last_used_at = ?
		  WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`),
		at.UTC(), grantID, at.Add(-store.OAuthTouchInterval).UTC())
	return err
}

func (s *Store) ListOAuthGrants(ctx context.Context, userID string) ([]store.OAuthGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		q(`SELECT g.id, g.user_id, u.name, g.client_id, g.client_name, g.scopes, g.resource,
		        g.created_at, g.last_used_at, g.revoked_at
		   FROM oauth_grants g JOIN users u ON u.id = g.user_id
		  WHERE (?::text = '' OR g.user_id = ?)
		  ORDER BY g.created_at DESC, g.id`), userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.OAuthGrant
	for rows.Next() {
		var g store.OAuthGrant
		var scopes string
		if err := rows.Scan(&g.ID, &g.UserID, &g.UserName, &g.ClientID, &g.ClientName, &scopes,
			&g.Resource, &g.CreatedAt, &g.LastUsedAt, &g.RevokedAt); err != nil {
			return nil, err
		}
		g.Scopes = strings.Fields(scopes)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) RevokeOAuthGrant(ctx context.Context, userID, grantID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		q(`UPDATE oauth_grants SET revoked_at = COALESCE(revoked_at, ?::timestamptz)
		  WHERE id = ? AND (?::text = '' OR user_id = ?)`),
		at.UTC(), grantID, userID, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
