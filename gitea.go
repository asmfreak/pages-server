package main

import (
	"context"

	"code.gitea.io/sdk/gitea"
)

func allGiteaPages[T any](ctx context.Context, content func(ctx context.Context, opts gitea.ListOptions) ([]T, *gitea.Response, error)) (ret []T, err error) {
	pagesDone := false
	page := 1
	for !pagesDone {
		var rsp *gitea.Response
		var pageData []T
		pageData, rsp, err = content(ctx, gitea.ListOptions{
			Page:     page,
			PageSize: 100,
		})
		if err != nil {
			return
		}
		ret = append(ret, pageData...)
		if page >= rsp.LastPage {
			pagesDone = true
		}
		page++
	}
	return
}
