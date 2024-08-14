package types

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

type GiteaUID int64

type UserSession struct {
	GiteaUID GiteaUID `json:"gitea_uid"`
}

type User struct {
	GiteaUID   GiteaUID      `json:"gitea_uid"`
	Token      *oauth2.Token `json:"token"`
	HasWebhook bool          `json:"has_webhook"`
}

type PageSHA256 string

func (p PageSHA256) String() string {
	return string(p)
}

func HashPage(data []byte) PageSHA256 {
	hasher := sha256.New()
	hasher.Write(data)
	return PageSHA256(strings.ToLower(fmt.Sprintf("%x", hasher.Sum(nil))))
}

type PagesSHA256 string

func HashPagesFile(f io.Reader) (PagesSHA256, error) {
	hasher := sha256.New()
	_, err := io.Copy(hasher, f)
	if err != nil {
		err = fmt.Errorf("failed to hash: %w", err)
		return "", err
	}
	return PagesSHA256(strings.ToLower(fmt.Sprintf("%x", hasher.Sum(nil)))), nil
}

func PagesSHA256FromString(s string) PagesSHA256 {
	return PagesSHA256(strings.ToLower(s))
}

func (p PagesSHA256) String() string {
	return string(p)
}

type (
	Pages    []PageFile
	PageFile struct {
		Name string     `json:"name"`
		SHA  PageSHA256 `json:"sha"`
	}
)

type Version struct {
	Version   string      `json:"name"`
	CreatedAt time.Time   `json:"created_at"`
	SHA       PagesSHA256 `json:"sha"`
}

type Repo struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

func (r *Repo) String() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Repo)
}

func (r *Repo) Parse(s string) error {
	owner, repo, ok := strings.Cut(s, "/")
	if !ok {
		return fmt.Errorf("failed to parse job %s", s)
	}
	r.Owner, r.Repo = owner, repo
	return nil
}

type RepoInfo struct {
	Repo     Repo      `json:"repo"`
	Latest   Version   `json:"latest"`
	Versions []Version `json:"versions"`
}

type RepoVersion struct {
	Repo    Repo   `json:"repo"`
	Version string `in:"query=version"`
	File    string `in:"path=*"`
}
