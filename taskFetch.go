package main

import (
	"context"
	"fmt"

	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
)

func fetchRepo(r types.Repo, rt types.RepoType, q *database.Queue) error {
	var fetchRequest database.TaskElement
	switch rt {
	case types.RepoTypeBranch:
		fr := FetchRepoFromBranches(r)
		fetchRequest = &fr
	case types.RepoTypePackage:
		fr := FetchRepoFromPackages(r)
		fetchRequest = &fr
	case types.RepoTypeRelease:
		fr := FetchRepoFromReleases(r)
		fetchRequest = &fr
	default:
		panic(fmt.Sprintf("unexpected types.RepoType: %#v", rt))
	}
	return q.Enqueue(context.Background(), fetchRequest)
}

func fetchVersion(r types.Repo, v types.Version, rt types.RepoType, q *database.Queue) error {
	var fetchRequest database.TaskElement
	switch rt {
	case types.RepoTypeBranch:
		fr := FetchVersionFromBranches{
			Repo: r, Version: v,
		}
		fetchRequest = &fr
	case types.RepoTypePackage:
		fr := FetchVersionFromPackages{
			Repo: r, Version: v,
		}
		fetchRequest = &fr
	case types.RepoTypeRelease:
		fr := FetchVersionFromReleases{
			Repo: r, Version: v,
		}
		fetchRequest = &fr
	default:
		panic(fmt.Sprintf("unexpected types.RepoType: %#v", rt))
	}
	return q.Enqueue(context.Background(), fetchRequest)
}
