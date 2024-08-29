package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"code.gitea.io/sdk/gitea"
	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
)

type FetchRepoFromPackages types.Repo

// QueueName implements database.TaskElement.
func (f *FetchRepoFromPackages) QueueName() string {
	return "fetchRepoFromPackages"
}

// DedupingKey implements database.TaskElement.
func (f *FetchRepoFromPackages) DedupingKey() string {
	return fmt.Sprintf("fetch[%s]", (*types.Repo)(f).String())
}

// Job implements database.TaskElement.
func (f *FetchRepoFromPackages) Job() string {
	return (*types.Repo)(f).String()
}

// ParseJob implements database.TaskElement.
func (f *FetchRepoFromPackages) ParseJob(s string) error {
	var ok bool
	f.Owner, f.Repo, ok = strings.Cut(s, "/")
	if !ok {
		return fmt.Errorf("failed to parse job %s", s)
	}
	return nil
}

var _ database.TaskElement = (*FetchRepoFromPackages)(nil)

func fetchRepoFromPackages(c *gitea.Client, db *database.Database) database.Task {
	return database.FuncTask(func(ctx context.Context, task *FetchRepoFromPackages) error {
		slog.Info("fetching repo from packages", slog.String("owner", task.Owner), slog.String("repo", task.Repo))
		repoInfo := types.RepoInfo{
			Repo: types.Repo(*task),
		}
		versions, err := allGiteaPages(ctx, func(_ context.Context, opts gitea.ListOptions) ([]types.Version, *gitea.Response, error) {
			packages, resp, err := c.ListPackages(task.Owner, gitea.ListPackagesOptions{
				ListOptions: opts,
			})
			if err != nil {
				err = fmt.Errorf("failed to list packages %w", err)
				return nil, nil, err
			}
			var ret []types.Version
			for _, pkg := range packages {
				if pkg.Type != "generic" {
					continue
				}
				if pkg.Name != task.Repo {
					continue
				}
				packageFiles, _, err := c.ListPackageFiles(task.Owner, pkg.Type, pkg.Name, pkg.Version)
				if err != nil {
					err = fmt.Errorf("failed to list packages files %w", err)
					return nil, nil, err
				}
				var files []*gitea.PackageFile
				for _, file := range packageFiles {
					if file.Name != "docs.zip" {
						continue
					}
					files = append(files, file)
				}
				if len(files) == 0 {
					continue
				}
				if len(files) > 1 {
					slog.Warn("found several docs.zip", slog.String("owner", task.Owner), slog.String("repo", task.Repo), slog.String("version", pkg.Version))
				}
				file := files[0]
				ret = append(ret, types.Version{
					Version:   pkg.Version,
					CreatedAt: pkg.CreatedAt,
					SHA:       types.PagesSHA256FromString(file.SHA256),
				})
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
			err := q.Enqueue(ctx, &FetchVersionFromPackages{Repo: types.Repo(*task), Version: v})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
