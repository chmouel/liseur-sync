package sqlite

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
		`INSERT INTO oauth_clients (id, kind, name, redirect_uris, secret_sha256, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.Kind, c.Name, strings.Join(c.RedirectURIs, "\n"), secret, formatTime(c.CreatedAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

const oauthClientColumns = `id, kind, name, redirect_uris, secret_sha256, created_at`

func scanOAuthClient(row interface{ Scan(...any) error }) (store.OAuthClient, error) {
	var c store.OAuthClient
	var uris, created string
	var secret sql.NullString
	if err := row.Scan(&c.ID, &c.Kind, &c.Name, &uris, &secret, &created); err != nil {
		return c, err
	}
	c.RedirectURIs = splitLines(uris)
	c.SecretSHA256 = secret.String
	var err error
	c.CreatedAt, err = parseTime(created)
	return c, err
}

func (s *Store) OAuthClient(ctx context.Context, id string) (store.OAuthClient, error) {
	c, err := scanOAuthClient(s.db.QueryRowContext(ctx,
		`SELECT `+oauthClientColumns+` FROM oauth_clients WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, store.ErrNotFound
	}
	return c, err
}

func (s *Store) ListOAuthClients(ctx context.Context) ([]store.OAuthClient, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+oauthClientColumns+` FROM oauth_clients ORDER BY created_at, id`)
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM oauth_clients WHERE id = ?`, id)
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
		`INSERT INTO oauth_codes (sha256, user_id, client_id, client_name, redirect_uri,
		   code_challenge, resource, scopes, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.SHA256, c.UserID, c.ClientID, c.ClientName, c.RedirectURI, c.CodeChallenge,
		c.Resource, strings.Join(c.Scopes, " "), formatTime(c.CreatedAt), formatTime(c.ExpiresAt))
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
	var userID, clientID, clientName, redirectURI, challenge, resource, scopes, expires string
	var used, codeGrant sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, client_id, client_name, redirect_uri, code_challenge, resource,
		        scopes, expires_at, used_at, grant_id
		   FROM oauth_codes WHERE sha256 = ?`, r.SHA256).
		Scan(&userID, &clientID, &clientName, &redirectURI, &challenge, &resource,
			&scopes, &expires, &used, &codeGrant)
	if errors.Is(err, sql.ErrNoRows) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if err != nil {
		return store.OAuthGrant{}, err
	}
	if used.Valid {
		return store.OAuthGrant{}, revokeSpentCode(ctx, tx, r.SHA256, now)
	}
	expiresAt, err := parseTime(expires)
	if err != nil {
		return store.OAuthGrant{}, err
	}
	if !now.Before(expiresAt) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if clientID != r.ClientID || redirectURI != r.RedirectURI || challenge != r.CodeChallenge ||
		(r.Resource != "" && r.Resource != resource) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	// A concurrent use that got there first leaves nothing to update,
	// and this one loses exactly as a later replay would.
	res, err := tx.ExecContext(ctx,
		`UPDATE oauth_codes SET used_at = ? WHERE sha256 = ? AND used_at IS NULL`, formatTime(now.UTC()), r.SHA256)
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
		`INSERT INTO oauth_grants (id, user_id, client_id, client_name, scopes, resource, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.UserID, g.ClientID, g.ClientName, strings.Join(g.Scopes, " "), g.Resource,
		formatTime(g.CreatedAt)); err != nil {
		if isUniqueErr(err) {
			return store.OAuthGrant{}, store.ErrConflict
		}
		return store.OAuthGrant{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE oauth_codes SET grant_id = ? WHERE sha256 = ?`, g.ID, r.SHA256); err != nil {
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
	err := tx.QueryRowContext(ctx, `SELECT grant_id FROM oauth_codes WHERE sha256 = ?`, sha256).Scan(&grant)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	if grant.Valid {
		if _, err := tx.ExecContext(ctx,
			`UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
			formatTime(now.UTC()), grant.String); err != nil {
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
			`INSERT INTO oauth_tokens (sha256, grant_id, kind, created_at, expires_at)
			 VALUES (?, ?, ?, ?, ?)`,
			t.SHA256, grantID, t.Kind, formatTime(t.CreatedAt), formatTime(t.ExpiresAt)); err != nil {
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
	var scopes, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT g.id, g.user_id, g.client_id, g.scopes, g.resource, t.expires_at
		   FROM oauth_tokens t
		   JOIN oauth_grants g ON g.id = t.grant_id
		   JOIN users u ON u.id = g.user_id
		  WHERE t.sha256 = ? AND t.kind = 'access' AND t.expires_at > ?
		    AND g.revoked_at IS NULL AND u.disabled_at IS NULL`,
		sha256, formatTime(now.UTC())).
		Scan(&a.GrantID, &a.UserID, &a.ClientID, &scopes, &a.Resource, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return a, store.ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.Scopes = strings.Fields(scopes)
	a.ExpiresAt, err = parseTime(expires)
	return a, err
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
	nowS := formatTime(now.UTC())
	var used sql.NullString
	var expires, scopes, created string
	var disabled, revoked sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT t.used_at, t.expires_at, g.id, g.user_id, g.client_id, g.client_name,
		        g.scopes, g.resource, g.created_at, g.revoked_at, u.disabled_at
		   FROM oauth_tokens t
		   JOIN oauth_grants g ON g.id = t.grant_id
		   JOIN users u ON u.id = g.user_id
		  WHERE t.sha256 = ? AND t.kind = 'refresh'`, sha256).
		Scan(&used, &expires, &g.ID, &g.UserID, &g.ClientID, &g.ClientName,
			&scopes, &g.Resource, &created, &revoked, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if err != nil {
		return g, err
	}
	if g.ClientID != clientID {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	if used.Valid {
		if _, err := tx.ExecContext(ctx,
			`UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
			nowS, g.ID); err != nil {
			return g, err
		}
		if err := tx.Commit(); err != nil {
			return g, err
		}
		return store.OAuthGrant{}, store.ErrConflict
	}
	if revoked.Valid || disabled.Valid || expires <= nowS {
		return store.OAuthGrant{}, store.ErrNotFound
	}
	// A concurrent use that got there first leaves nothing to update,
	// and this one loses exactly as a later replay would.
	res, err := tx.ExecContext(ctx,
		`UPDATE oauth_tokens SET used_at = ? WHERE sha256 = ? AND used_at IS NULL`, nowS, sha256)
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
			`UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, nowS, g.ID); err != nil {
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
		`DELETE FROM oauth_tokens WHERE grant_id = ? AND kind = 'access'`, g.ID); err != nil {
		return g, err
	}
	if err := insertOAuthTokens(ctx, tx, g.ID, tokens); err != nil {
		return g, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE oauth_grants SET last_used_at = ? WHERE id = ?`, nowS, g.ID); err != nil {
		return g, err
	}
	g.Scopes = strings.Fields(scopes)
	if g.CreatedAt, err = parseTime(created); err != nil {
		return g, err
	}
	g.LastUsedAt = &now
	return g, tx.Commit()
}

func (s *Store) TouchOAuthGrant(ctx context.Context, grantID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE oauth_grants SET last_used_at = ?
		  WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		formatTime(at.UTC()), grantID, formatTime(at.Add(-store.OAuthTouchInterval).UTC()))
	return err
}

func (s *Store) ListOAuthGrants(ctx context.Context, userID string) ([]store.OAuthGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT g.id, g.user_id, u.name, g.client_id, g.client_name, g.scopes, g.resource,
		        g.created_at, g.last_used_at, g.revoked_at
		   FROM oauth_grants g JOIN users u ON u.id = g.user_id
		  WHERE (? = '' OR g.user_id = ?)
		  ORDER BY g.created_at DESC, g.id`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.OAuthGrant
	for rows.Next() {
		var g store.OAuthGrant
		var scopes, created string
		var last, revoked sql.NullString
		if err := rows.Scan(&g.ID, &g.UserID, &g.UserName, &g.ClientID, &g.ClientName, &scopes,
			&g.Resource, &created, &last, &revoked); err != nil {
			return nil, err
		}
		g.Scopes = strings.Fields(scopes)
		if g.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if g.LastUsedAt, err = parseTimePtr(last); err != nil {
			return nil, err
		}
		if g.RevokedAt, err = parseTimePtr(revoked); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) RevokeOAuthGrant(ctx context.Context, userID, grantID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE oauth_grants SET revoked_at = COALESCE(revoked_at, ?)
		  WHERE id = ? AND (? = '' OR user_id = ?)`,
		formatTime(at.UTC()), grantID, userID, userID)
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
