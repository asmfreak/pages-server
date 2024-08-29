package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"code.gitea.io/sdk/gitea"
	"github.com/ASMfreaK/pages-server/pages-server/consts"
	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
)

type FetchRepoFromReleases types.Repo

// QueueName implements database.TaskElement.
func (f *FetchRepoFromReleases) QueueName() string {
	return "fetchRepoFromReleases"
}

// DedupingKey implements database.TaskElement.
func (f *FetchRepoFromReleases) DedupingKey() string {
	return fmt.Sprintf("fetch[%s]", (*types.Repo)(f).String())
}

// Job implements database.TaskElement.
func (f *FetchRepoFromReleases) Job() string {
	return (*types.Repo)(f).String()
}

// ParseJob implements database.TaskElement.
func (f *FetchRepoFromReleases) ParseJob(s string) error {
	var ok bool
	f.Owner, f.Repo, ok = strings.Cut(s, "/")
	if !ok {
		return fmt.Errorf("failed to parse job %s", s)
	}
	return nil
}

var _ database.TaskElement = (*FetchRepoFromReleases)(nil)

func fetchRepoFromReleases(c *gitea.Client, db *database.Database) database.Task {
	return database.FuncTask(func(ctx context.Context, task *FetchRepoFromReleases) error {
		slog.Info("fetching repo from releases", slog.String("owner", task.Owner), slog.String("repo", task.Repo))
		repoInfo := types.RepoInfo{
			Repo: types.Repo(*task),
		}
		versions, err := allGiteaPages(ctx, func(_ context.Context, opts gitea.ListOptions) ([]types.Version, *gitea.Response, error) {
			releases, resp, err := c.ListReleases(task.Owner, task.Repo, gitea.ListReleasesOptions{
				ListOptions: opts,
			})
			if err != nil {
				err = fmt.Errorf("failed to list releases %w", err)
				return nil, nil, err
			}
			var ret []types.Version
			for _, release := range releases {
				if release.IsPrerelease || release.IsDraft {
					continue
				}
				files, err := allGiteaPages(ctx, func(_ context.Context, opts gitea.ListOptions) ([]*gitea.Attachment, *gitea.Response, error) {
					attachments, attachmentsResp, err := c.ListReleaseAttachments(task.Owner, task.Repo, release.ID, gitea.ListReleaseAttachmentsOptions{
						ListOptions: opts,
					})
					var files []*gitea.Attachment
					for _, file := range attachments {
						if file.Name != "docs.zip" {
							continue
						}
						files = append(files, file)
					}
					return files, attachmentsResp, err
				})
				if err != nil {
					return nil, nil, err
				}
				if len(files) == 0 {
					continue
				}
				if len(files) > 1 {
					slog.Warn("found several docs.zip", slog.String("owner", task.Owner), slog.String("repo", task.Repo), slog.String("version", release.TagName))
				}
				file := files[0]
				kindaSha := types.PagesSHA256FromString(file.UUID)
				ret = append(ret, types.Version{
					Version:   release.TagName,
					CreatedAt: release.CreatedAt,
					SHA:       kindaSha,
					Extra: map[string]any{
						consts.ReleaseID:           strconv.FormatInt(release.ID, 10),
						consts.ReleaseAttachmentID: strconv.FormatInt(file.ID, 10),
					},
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
			err := q.Enqueue(ctx, &FetchVersionFromReleases{Repo: types.Repo(*task), Version: v})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
