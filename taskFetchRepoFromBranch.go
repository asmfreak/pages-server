package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/ASMfreaK/pages-server/pages-server/consts"
	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
)

type FetchRepoFromBranches types.Repo

// QueueName implements database.TaskElement.
func (f *FetchRepoFromBranches) QueueName() string {
	return "fetchRepoFromBranch"
}

// DedupingKey implements database.TaskElement.
func (f *FetchRepoFromBranches) DedupingKey() string {
	return fmt.Sprintf("fetch[%s]", (*types.Repo)(f).String())
}

// Job implements database.TaskElement.
func (f *FetchRepoFromBranches) Job() string {
	return (*types.Repo)(f).String()
}

// ParseJob implements database.TaskElement.
func (f *FetchRepoFromBranches) ParseJob(s string) error {
	var ok bool
	f.Owner, f.Repo, ok = strings.Cut(s, "/")
	if !ok {
		return fmt.Errorf("failed to parse job %s", s)
	}
	return nil
}

var _ database.TaskElement = (*FetchRepoFromBranches)(nil)

func fetchRepoFromBranches(c *gitea.Client, db *database.Database) database.Task {
	return database.FuncTask(func(ctx context.Context, task *FetchRepoFromBranches) error {
		slog.Info("fetching repo from branches", slog.String("owner", task.Owner), slog.String("repo", task.Repo))
		repoInfo := types.RepoInfo{
			Repo: types.Repo(*task),
		}
		versions, err := allGiteaPages(ctx, func(_ context.Context, opts gitea.ListOptions) ([]types.Version, *gitea.Response, error) {
			branches, resp, err := c.ListRepoBranches(task.Owner, task.Repo, gitea.ListRepoBranchesOptions{
				ListOptions: opts,
			})
			if err != nil {
				err = fmt.Errorf("failed to list branches %w", err)
				return nil, nil, err
			}
			var ret []types.Version
			for _, branch := range branches {
				version := types.Version{
					CreatedAt: branch.Commit.Timestamp,
					SHA:       types.PagesSHA256FromString(branch.Commit.ID),
				}
				if branch.Name == consts.PagesBranch {
					version.Version = "latest"
					version.CreatedAt = time.Now()
				} else {
					version.Version = strings.TrimPrefix(branch.Name, consts.PagesBranchPrefix)
					if version.Version == branch.Name {
						continue
					}
				}
				ret = append(ret, version)
			}
			return ret, resp, nil
		})
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			slog.Error("no versions found for repo", "owner", task.Job())
			return nil
		}
		repoInfo.Versions = versions
		if len(repoInfo.Versions) != 0 {
			slices.SortFunc(repoInfo.Versions, func(a, b types.Version) int {
				return b.CreatedAt.Compare(a.CreatedAt)
			})
			repoInfo.Latest = repoInfo.Versions[0]
		}
		err = db.RepoPages().Set(types.Repo(*task), repoInfo)
		if err != nil {
			return err
		}
		q := database.QueueFromContext(ctx)
		for _, v := range repoInfo.Versions {
			err := q.Enqueue(ctx, &FetchVersionFromBranches{Repo: types.Repo(*task), Version: v})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
