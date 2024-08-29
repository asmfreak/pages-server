package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
)

var ErrFileNotFound = errors.New("file not found")

func requestPageData(r *types.RepoFileAtVersion, rt types.RepoType, db *database.Database, q *database.Queue) (data []byte, fetched bool, err error) {
	slog.Info("requesting page data", "repo", r)
	repoInfo, ok, err := db.RepoPages().Get(r.Repo)
	if err != nil {
		err = fmt.Errorf("failed to get repo info %w", err)
		return nil, false, err
	}
	if !ok {
		err = fetchRepo(r.Repo, rt, q)
		if err != nil {
			slog.Error("failed to enqueue fetch repo", "err", err)
		}
		return nil, false, nil
	}
	var version types.Version
	if r.Version == "" {
		version = repoInfo.Latest
	} else {
		fetched = false
		for _, v := range repoInfo.Versions {
			if v.Version == r.Version {
				version = v
				fetched = true
				break
			}
		}
		if !fetched {
			return nil, false, nil
		}
	}
	if version.SHA == "" {
		return nil, false, nil
	}
	pages, ok, err := db.PagesMetadata().Get(version.SHA)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		err = fetchVersion(r.Repo, version, rt, q)
		return nil, false, err
	}

	if r.File == "" {
		r.File = "index.html"
	}
	if strings.HasSuffix(r.File, "/") {
		r.File += "index.html"
	}
	var fileSha types.PageSHA256
	for _, file := range pages {
		if file.Name == r.File {
			fileSha = file.SHA
			break
		}
	}
	if fileSha == "" {
		return nil, false, ErrFileNotFound
	}

	return db.PagesData().Get(fileSha)
}
