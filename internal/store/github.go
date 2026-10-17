package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GitHubSource liga um app a um repositório do GitHub. Secret e DeployKey ficam cifrados (crypt.Box) no banco.
type GitHubSource struct {
	AppID      int64
	URL        string
	Branch     string
	Secret     []byte
	DeployKey  []byte // nil: repositório público
	DeployPub  string
	LastSyncAt *int64
	LastError  string
	CreatedAt  int64
}

const ghCols = `app_id,url,branch,secret,deploy_key,deploy_pub,last_sync_at,last_error,created_at`

// GitHubSource devolve ErrNotFound quando o app só recebe git push.
func (s *Store) GitHubSource(ctx context.Context, appID int64) (GitHubSource, error) {
	var g GitHubSource
	err := s.DB.QueryRowContext(ctx, `select `+ghCols+` from github_sources where app_id=?`, appID).
		Scan(&g.AppID, &g.URL, &g.Branch, &g.Secret, &g.DeployKey, &g.DeployPub, &g.LastSyncAt, &g.LastError, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

func (s *Store) SetGitHubSource(ctx context.Context, g GitHubSource) error {
	if g.CreatedAt == 0 {
		g.CreatedAt = time.Now().UnixMilli()
	}
	_, err := s.DB.ExecContext(ctx, `insert into github_sources(`+ghCols+`) values(?,?,?,?,?,?,?,?,?)
		on conflict(app_id) do update set url=excluded.url, branch=excluded.branch, secret=excluded.secret,
		deploy_key=excluded.deploy_key, deploy_pub=excluded.deploy_pub, last_error=''`,
		g.AppID, g.URL, g.Branch, g.Secret, g.DeployKey, g.DeployPub, g.LastSyncAt, g.LastError, g.CreatedAt)
	return err
}

func (s *Store) SetGitHubSecret(ctx context.Context, appID int64, sealed []byte) error {
	_, err := s.DB.ExecContext(ctx, `update github_sources set secret=? where app_id=?`, sealed, appID)
	return err
}

func (s *Store) SetGitHubSync(ctx context.Context, appID int64, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `update github_sources set last_sync_at=?, last_error=? where app_id=?`, time.Now().UnixMilli(), errMsg, appID)
	return err
}

func (s *Store) DeleteGitHubSource(ctx context.Context, appID int64) error {
	_, err := s.DB.ExecContext(ctx, `delete from github_sources where app_id=?`, appID)
	return err
}
