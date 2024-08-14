package database

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ASMfreaK/pages-server/pages-server/consts"
	"github.com/ASMfreaK/pages-server/pages-server/database/sharedbbolt"
	"github.com/ASMfreaK/pages-server/pages-server/types"
	"github.com/go-chi/jwtauth/v5"
	"github.com/hashicorp/go-multierror"
	"github.com/oklog/ulid/v2"
	"github.com/philippgille/gokv"
	"github.com/philippgille/gokv/encoding"
	"github.com/philippgille/gokv/syncmap"
)

func key(k any) string {
	switch k := k.(type) {
	case string:
		return k
	case fmt.Stringer:
		return k.String()
	default:
		return fmt.Sprintf("%v", k)
	}
}

type Store[K /*stringlike*/ any, T any] interface {
	Set(k K, v T) error
	Get(k K) (value T, found bool, err error)
	Delete(k K) error
	Close() error
}

type store[K any, T any] struct {
	store gokv.Store
}

func (s *store[K, T]) Set(k K, v T) error {
	return s.store.Set(key(k), v)
}

func (s *store[K, T]) Get(k K) (value T, found bool, err error) {
	var v T
	found, err = s.store.Get(key(k), &v)
	if err != nil {
		return v, false, err
	}
	return v, found, nil
}

func (s *store[K, T]) Delete(k K) error {
	return s.store.Delete(key(k))
}

func (s *store[K, T]) Close() error {
	return s.store.Close()
}

type Database struct {
	userSessions  Store[ulid.ULID, types.UserSession]
	users         Store[types.GiteaUID, types.User]
	repoPages     Store[types.Repo, types.RepoInfo]
	pagesMetadata Store[types.PagesSHA256, types.Pages]
	pagesData     Store[types.PageSHA256, []byte]
}

type noopEncoding struct{}

func (noopEncoding) Marshal(v any) ([]byte, error) {
	return v.([]byte), nil
}

func (noopEncoding) Unmarshal(data []byte, v any) error {
	*v.(*[]byte) = data
	return nil
}

var _ encoding.Codec = (*noopEncoding)(nil)

func New(params Params) (*Database, error) {
	db, err := sharedbbolt.NewSharedState(params.Filename)
	if err != nil {
		return nil, err
	}
	pagesData, err := db.NewStore(sharedbbolt.Options{
		BucketName: "pages-data",
		Codec:      &noopEncoding{},
	})
	if err != nil {
		return nil, err
	}
	pagesMetadata, err := db.NewStore(sharedbbolt.Options{
		BucketName: "pages-meta",
		Codec:      encoding.JSON,
	})
	if err != nil {
		return nil, err
	}
	repoPages, err := db.NewStore(sharedbbolt.Options{
		BucketName: "repo-pages",
		Codec:      encoding.JSON,
	})
	if err != nil {
		return nil, err
	}
	users, err := db.NewStore(sharedbbolt.Options{
		BucketName: "users",
		Codec:      encoding.JSON,
	})
	if err != nil {
		return nil, err
	}
	return &Database{
		userSessions: &store[ulid.ULID, types.UserSession]{
			store: syncmap.NewStore(
				syncmap.Options{
					Codec: encoding.JSON,
				},
			),
		},
		users:         &store[types.GiteaUID, types.User]{users},
		repoPages:     &store[types.Repo, types.RepoInfo]{repoPages},
		pagesMetadata: &store[types.PagesSHA256, types.Pages]{pagesMetadata},
		pagesData:     &store[types.PageSHA256, []byte]{pagesData},
	}, nil
}

func (db *Database) Close() error {
	return multierror.Append(
		db.userSessions.Close(),
		db.users.Close(),
		db.repoPages.Close(),
		db.pagesMetadata.Close(),
		db.pagesData.Close(),
	)
}

func (db *Database) UserSessions() Store[ulid.ULID, types.UserSession] {
	return db.userSessions
}

func (db *Database) Users() Store[types.GiteaUID, types.User] {
	return db.users
}

func (db *Database) RepoPages() Store[types.Repo, types.RepoInfo] {
	return db.repoPages
}

func (db *Database) PagesMetadata() Store[types.PagesSHA256, types.Pages] {
	return db.pagesMetadata
}

func (db *Database) PagesData() Store[types.PageSHA256, []byte] {
	return db.pagesData
}

func (db *Database) UserSessionFromToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, err := db.getUserSessionFromJwt(r.Context())
		ctx := NewUserSessionContext(r.Context(), user, err)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (db *Database) UserFromWebhookToken(key string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _, err := db.getUserSessionFromWebhookJwt(r.Context(), key)
			ctx := NewUserContext(r.Context(), user, err)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (db *Database) UserFromUserSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userSession, err := UserSessionFromContext(r.Context())
		ctx := r.Context()
		if err == nil {
			user, _, err := db.Users().Get(userSession.GiteaUID)
			ctx = NewUserContext(ctx, user, err)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type ctxKey struct {
	name string
}

var (
	userCtxKey        = &ctxKey{"user"}
	userSessionCtxKey = &ctxKey{"userSession"}
	errorCtxKey       = &ctxKey{"error"}
)

func NewUserSessionContext(ctx context.Context, user types.UserSession, err error) context.Context {
	ctx = context.WithValue(ctx, userSessionCtxKey, user)
	ctx = context.WithValue(ctx, errorCtxKey, err)
	return ctx
}

func UserSessionFromContext(ctx context.Context) (types.UserSession, error) {
	user := ctx.Value(userSessionCtxKey)
	err := ctx.Value(errorCtxKey)
	if user == nil && err == nil {
		return types.UserSession{}, errors.New("user not found")
	}
	ruser, _ := user.(types.UserSession)
	rerr, _ := err.(error)
	return ruser, rerr
}

func NewUserContext(ctx context.Context, user types.User, err error) context.Context {
	ctx = context.WithValue(ctx, userCtxKey, user)
	ctx = context.WithValue(ctx, errorCtxKey, err)
	return ctx
}

func UserFromContext(ctx context.Context) (types.User, error) {
	user := ctx.Value(userCtxKey)
	err := ctx.Value(errorCtxKey)
	if user == nil && err == nil {
		return types.User{}, errors.New("user not found")
	}
	ruser, _ := user.(types.User)
	rerr, _ := err.(error)
	return ruser, rerr
}

func (db *Database) getUserSessionFromJwt(ctx context.Context) (types.UserSession, bool, error) {
	_, claims, err := jwtauth.FromContext(ctx)
	if err != nil {
		return types.UserSession{}, false, err
	}

	maybeUserID, ok := claims[consts.UserID]
	if !ok {
		return types.UserSession{}, false, errors.New("token does not contain userId")
	}
	userID, ok := maybeUserID.(ulid.ULID)
	if !ok {
		return types.UserSession{}, false, errors.New("token userId is not a ULID")
	}

	return db.UserSessions().Get(userID)
}

func (db *Database) getUserSessionFromWebhookJwt(ctx context.Context, key string) (types.User, bool, error) {
	_, claims, err := jwtauth.FromContext(ctx)
	if err != nil {
		return types.User{}, false, err
	}

	maybeUserID, ok := claims[key]
	if !ok {
		return types.User{}, false, errors.New("token does not contain userId")
	}
	userID, ok := maybeUserID.(int64)
	if !ok {
		return types.User{}, false, errors.New("token userId is not a ULID")
	}

	return db.Users().Get(types.GiteaUID(userID))
}
