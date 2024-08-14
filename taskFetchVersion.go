package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
	"golang.org/x/sync/errgroup"
)

type FetchVersion struct {
	types.Repo
	Version types.Version
}

// QueueName implements database.TaskElement.
func (f *FetchVersion) QueueName() string {
	return "fetchVersion"
}

// DedupingKey implements database.TaskElement.
func (f *FetchVersion) DedupingKey() string {
	return fmt.Sprintf("fetchVersion[%s]", f.Version.SHA)
}

// Job implements database.TaskElement.
func (f *FetchVersion) Job() string {
	data, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ParseJob implements database.TaskElement.
func (f *FetchVersion) ParseJob(s string) error {
	err := json.Unmarshal([]byte(s), f)
	if err != nil {
		slog.Error("failed to parse FetchVersion", "err", err)
		return err
	}
	return nil
}

var _ database.TaskElement = (*FetchVersion)(nil)

func fetchVersion(g GiteaInfo, db *database.Database) database.Task {
	return database.FuncTask(func(ctx context.Context, task *FetchVersion) error {
		slog.Info("fetching version", "version", task)
		{
			_, ok, err := db.PagesMetadata().Get(task.Version.SHA)
			if err != nil {
				return err
			}
			if ok {
				slog.Info("version already fetched", "version", task)
				return nil
			}
		}
		url := fmt.Sprintf(
			"%s/api/packages/%s/generic/%s/%s/%s",
			strings.TrimSuffix(g.URL, "/"),
			task.Repo.Owner,
			task.Repo.Repo,
			task.Version.Version,
			"docs.zip",
		)
		rq, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
		rq.Header.Set("Authorization", fmt.Sprintf("token %s", g.AdminToken))
		rsp, err := http.DefaultClient.Do(rq)
		if err != nil {
			return err
		}
		defer rsp.Body.Close()
		if rsp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to fetch docs.zip from %s %s", url, rsp.Status)
		}
		f, err := os.CreateTemp("", "tmpfile-")
		if err != nil {
			log.Fatal(err)
		}

		// close and remove the temporary file at the end of the program
		defer f.Close()
		defer os.Remove(f.Name())

		fb := bufio.NewWriter(f)
		slog.Info("writing to a temp file", "name", f.Name())
		written, err := io.Copy(fb, rsp.Body)
		//  &progress{
		// 	Message: "fetching docs.zip",
		// 	Reader:  rsp.Body,
		// 	Total:   rsp.ContentLength,
		// })
		if err != nil {
			slog.Info("error writing to a temp file", "name", f.Name(), "err", err)
			return err
		}
		slog.Info("done writing to a temp file", "name", f.Name())
		if written != rsp.ContentLength {
			return fmt.Errorf("failed to write all data")
		}
		err = fb.Flush()
		if err != nil {
			err = fmt.Errorf("failed to flush: %w", err)
			return err
		}

		_, err = f.Seek(0, 0)
		if err != nil {
			err = fmt.Errorf("failed to seek: %w", err)
			return err
		}
		hash, err := types.HashPagesFile(bufio.NewReader(f))
		if err != nil {
			return err
		}
		if hash != task.Version.SHA {
			return fmt.Errorf("sha256 mismatch")
		}
		_, err = f.Seek(0, 0)
		if err != nil {
			err = fmt.Errorf("failed to seek: %w", err)
			return err
		}
		zr, err := zip.NewReader(f, written)
		if err != nil {
			return err
		}
		var files types.Pages
		var filesMux sync.Mutex
		eg, _ := errgroup.WithContext(ctx)
		eg.SetLimit(5)
		for _, file := range zr.File {
			eg.Go(func() error {
				f, ferr := file.Open()
				if ferr != nil {
					return ferr
				}
				defer f.Close()
				var buf bytes.Buffer
				_, ferr = io.Copy(&buf, f)
				if ferr != nil {
					return ferr
				}
				data := buf.Bytes()
				hash := types.HashPage(data)
				ferr = db.PagesData().Set(hash, data)
				if ferr != nil {
					ferr = fmt.Errorf("failed to set page data: %w", ferr)
					return ferr
				}

				filesMux.Lock()
				defer filesMux.Unlock()
				// slog.Info("found file", "name", file.Name, "hash", hash)
				files = append(files, types.PageFile{
					Name: file.Name,
					SHA:  hash,
				})
				return nil
			})
		}
		err = eg.Wait()
		if err != nil {
			return err
		}
		err = db.PagesMetadata().Set(task.Version.SHA, files)
		if err != nil {
			return err
		}
		return nil
	})
}
