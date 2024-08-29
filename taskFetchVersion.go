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
	"strconv"
	"strings"
	"sync"

	"code.gitea.io/sdk/gitea"
	"github.com/ASMfreaK/pages-server/pages-server/consts"
	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
	"golang.org/x/sync/errgroup"
)

type FetchVersionFromPackages struct {
	types.Repo
	Version types.Version
}

// QueueName implements database.TaskElement.
func (f *FetchVersionFromPackages) QueueName() string {
	return "fetchVersionFromPackages"
}

// DedupingKey implements database.TaskElement.
func (f *FetchVersionFromPackages) DedupingKey() string {
	return fmt.Sprintf("fetchVersion[%s]", f.Version.SHA)
}

// Job implements database.TaskElement.
func (f *FetchVersionFromPackages) Job() string {
	data, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ParseJob implements database.TaskElement.
func (f *FetchVersionFromPackages) ParseJob(s string) error {
	err := json.Unmarshal([]byte(s), f)
	if err != nil {
		slog.Error("failed to parse FetchVersion", "err", err)
		return err
	}
	return nil
}

var _ database.TaskElement = (*FetchVersionFromPackages)(nil)

func fetchVersionFromPackages(g GiteaInfo, db *database.Database) database.Task {
	return database.FuncTask(func(ctx context.Context, task *FetchVersionFromPackages) error {
		slog.Info("fetching version from packages", "version", task)
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
		files, err := unzipDocs(ctx, f, written, db)
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

type ReleaseID struct {
	ID         int64
	Attachment int64
}

type FetchVersionFromReleases struct {
	types.Repo
	Version types.Version
}

// QueueName implements database.TaskElement.
func (f *FetchVersionFromReleases) QueueName() string {
	return "fetchVersionFromReleases"
}

// DedupingKey implements database.TaskElement.
func (f *FetchVersionFromReleases) DedupingKey() string {
	return fmt.Sprintf("fetchVersion[%s]", f.Version.SHA)
}

// Job implements database.TaskElement.
func (f *FetchVersionFromReleases) Job() string {
	data, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ParseJob implements database.TaskElement.
func (f *FetchVersionFromReleases) ParseJob(s string) error {
	err := json.Unmarshal([]byte(s), f)
	if err != nil {
		slog.Error("failed to parse FetchVersion", "err", err)
		return err
	}
	return nil
}

var _ database.TaskElement = (*FetchVersionFromReleases)(nil)

func fetchVersionFromReleases(c *gitea.Client, g GiteaInfo, db *database.Database) database.Task {
	return database.FuncTask(func(ctx context.Context, task *FetchVersionFromReleases) error {
		slog.Info("fetching version from packages", "version", task)
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
		releaseID, err := strconv.ParseInt(task.Version.Extra[consts.ReleaseID].(string), 10, 64)
		if err != nil {
			return fmt.Errorf("failed to get release id: %w", err)
		}
		attachmentID, err := strconv.ParseInt(task.Version.Extra[consts.ReleaseAttachmentID].(string), 10, 64)
		if err != nil {
			return fmt.Errorf("failed to get release id: %w", err)
		}
		a, _, err := c.GetReleaseAttachment(task.Repo.Owner, task.Repo.Repo, releaseID, attachmentID)
		if err != nil {
			return err
		}
		rq, err := http.NewRequest("GET", a.DownloadURL, nil)
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
			return fmt.Errorf("failed to fetch docs.zip from %s %s", a.DownloadURL, rsp.Status)
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
		// hash, err := types.HashPagesFile(bufio.NewReader(f))
		// if err != nil {
		// 	return err
		// }

		_, err = f.Seek(0, 0)
		if err != nil {
			err = fmt.Errorf("failed to seek: %w", err)
			return err
		}
		files, err := unzipDocs(ctx, f, written, db)
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

type FetchVersionFromBranches struct {
	types.Repo
	Version types.Version
}

// QueueName implements database.TaskElement.
func (f *FetchVersionFromBranches) QueueName() string {
	return "fetchVersionFromBranches"
}

// DedupingKey implements database.TaskElement.
func (f *FetchVersionFromBranches) DedupingKey() string {
	return fmt.Sprintf("fetchVersion[%s]", f.Version.SHA)
}

// Job implements database.TaskElement.
func (f *FetchVersionFromBranches) Job() string {
	data, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ParseJob implements database.TaskElement.
func (f *FetchVersionFromBranches) ParseJob(s string) error {
	err := json.Unmarshal([]byte(s), f)
	if err != nil {
		slog.Error("failed to parse FetchVersion", "err", err)
		return err
	}
	return nil
}

var _ database.TaskElement = (*FetchVersionFromBranches)(nil)

func fetchVersionFromBranches(c *gitea.Client, db *database.Database) database.Task {
	return database.FuncTask(func(ctx context.Context, task *FetchVersionFromBranches) error {
		slog.Info("fetching version from packages", "version", task)
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
		data, _, err := c.GetArchive(task.Repo.Owner, task.Repo.Repo, string(task.Version.SHA), gitea.ZipArchive)
		if err != nil {
			return err
		}
		f, err := os.CreateTemp("", "tmpfile-")
		if err != nil {
			log.Fatal(err)
		}

		// close and remove the temporary file at the end of the program
		defer f.Close()
		defer os.Remove(f.Name())

		dataLen := int64(len(data))
		dataBuffer := bytes.NewBuffer(data)
		fb := bufio.NewWriter(f)
		slog.Info("writing to a temp file", "name", f.Name())
		written, err := io.Copy(fb, dataBuffer)
		if err != nil {
			slog.Info("error writing to a temp file", "name", f.Name(), "err", err)
			return err
		}
		slog.Info("done writing to a temp file", "name", f.Name())
		if written != dataLen {
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

		_, err = f.Seek(0, 0)
		if err != nil {
			err = fmt.Errorf("failed to seek: %w", err)
			return err
		}
		files, err := unzipDocs(ctx, f, written, db, unzipStripComponents(1))
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

type unzipDocsOptions struct {
	stripComponents int
}

type unzipDocsOption func(o *unzipDocsOptions)

func unzipStripComponents(s int) unzipDocsOption {
	return func(o *unzipDocsOptions) {
		o.stripComponents = s
	}
}

func unzipDocs(ctx context.Context, f io.ReaderAt, fSize int64, db *database.Database, optFuncs ...unzipDocsOption) (types.Pages, error) {
	var opts unzipDocsOptions
	for _, f := range optFuncs {
		f(&opts)
	}
	zr, err := zip.NewReader(f, fSize)
	if err != nil {
		return nil, err
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
			saveName := file.Name
			if opts.stripComponents > 0 {
				c := opts.stripComponents
				idx := strings.IndexFunc(saveName, func(ch rune) bool {
					if ch == '/' {
						c--
						if c == 0 {
							return true
						}
					}
					return false
				})
				if idx+1 == len(saveName) {
					slog.Info("file ignored (stripped components", "name", file.Name)
					return nil
				}
				saveName = saveName[idx+1:]
			}
			slog.Info("found file", "name", file.Name, "hash", hash, "saveName", saveName)
			files = append(files, types.PageFile{
				Name: saveName,
				SHA:  hash,
			})
			return nil
		})
	}
	err = eg.Wait()
	if err != nil {
		return nil, err
	}
	return files, nil
}
