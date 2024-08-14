package main

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	clive "github.com/ASMfreaK/clive2"
	"github.com/ASMfreaK/pages-server/pages-server/ccli"
	"github.com/ASMfreaK/pages-server/pages-server/consts"
	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v2"
	"github.com/go-chi/jwtauth/v5"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/oklog/ulid/v2"
	"github.com/urfave/cli/v2"
	"golang.org/x/oauth2"
)

type GiteaInfo struct {
	URL        string `cli:"usage:'url for Gitea',default:'http://localhost:3000'"`
	AdminToken string `cli:"usage:'admin token for Gitea'"`

	HookSecret string `cli:"usage:'secret for gitea webhooks'"`

	PagesAddrFromGitea string `cli:"usage:'url for pages server as viewed from gitea',default:'http://localhost:8000'"`
}

func (g *GiteaInfo) HookURL() string {
	return strings.TrimSuffix(g.PagesAddrFromGitea, "/") + consts.HookPath
}

type PagesInfo struct {
	URL   string `cli:"usage:'url for pages server',default:'http://localhost:8000'"`
	Title string `cli:"usage:'title for pages server',default:'Gitea Pages'"`
}

type GiteaPagesInfo struct {
	Gitea GiteaInfo
	Pages PagesInfo
}

var version = "dev"

type app struct {
	*clive.Command `cli:"name:'pages-server',usage:'pages-server simple pages server for small-to-medium gitea installations'"`
	Pages          PagesInfo `cli:"inline"`
	Gitea          GiteaInfo `cli:"inline"`

	Database database.Params `cli:"inline"`

	Auth AuthInfo `cli:"inline"`

	Server struct {
		Addr string `cli:"usage:'address to listen on',default:'localhost:8000'"`
	} `cli:"inline"`
}

func (a *app) Version() string {
	return version
}

func (a *app) Action(ctx *cli.Context) error {
	slog.SetDefault(slog.New(httplog.NewPrettyHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: false,
	})))
	logger := httplog.NewLogger("httplog-example", httplog.Options{
		// JSON:             true,
		LogLevel:         slog.LevelDebug,
		Writer:           os.Stdout,
		Concise:          true,
		MessageFieldName: "message",
		QuietDownRoutes: []string{
			"/",
			"/ping",
		},
		QuietDownPeriod: 10 * time.Second,
	})

	slog.Info(
		"pages server is starting",
	)
	jwt.RegisterCustomField(consts.UserID, ulid.ULID{})
	jwt.RegisterCustomField(consts.ThisIsAGiteaWebhook, true)
	jwt.RegisterCustomField(a.Auth.CookieName, int64(0))

	slog.Info("Initializing database")
	db, err := database.New(a.Database)
	if err != nil {
		return fmt.Errorf("failed to create database %w", err)
	}

	slog.Info("Initializing oauth")
	a.Auth.Initialize(a.Pages, a.Gitea, db)

	slog.Info("Initializing gitea admin client")
	c, err := gitea.NewClient(a.Gitea.URL, gitea.SetToken(a.Gitea.AdminToken))
	if err != nil {
		return fmt.Errorf("failed to create gitea client %w", err)
	}

	slog.Info("Initializing queue")
	q, err := database.NewQueue(ctx.Context, fetchRepo(c, db), fetchVersion(a.Gitea, db))
	if err != nil {
		return fmt.Errorf("failed to create queue %w", err)
	}

	slog.Info("Creating router")
	// Service
	r := chi.NewRouter()
	r.Use(httplog.RequestLogger(logger))
	r.Use(middleware.Heartbeat("/ping"))

	authdClient := authenticatedGiteaClient(a.Gitea.URL, a.Auth.State.oauthConfig, db, GiteaPagesInfo{a.Gitea, a.Pages})
	r.With(
		a.Auth.State.oauthStateVerrifier,
		tokenAuthenticator(GiteaPagesInfo{a.Gitea, a.Pages}),
		db.UserSessionFromToken, db.UserFromUserSession,
		authdClient,
	).Get("/", func(w http.ResponseWriter, _ *http.Request) {
		indexPage(GiteaPagesInfo{a.Gitea, a.Pages}, w)
	})

	r.Route(consts.HookPath, func(r chi.Router) {
		r.Post("/{owner:^[^_].*}/{repo:^[^_].*}", func(w http.ResponseWriter, r *http.Request) {
			owner := chi.URLParam(r, "owner")
			repoName := chi.URLParam(r, "repo")
			err := q.Enqueue(r.Context(), &FetchRepo{
				Owner: owner,
				Repo:  repoName,
			})
			if err != nil {
				slog.Error("failed to enqueue fetch repo", "err", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("ok"))
		})
	})

	// r.With(
	// 	a.Auth.State.oauthStateVerrifier,
	// 	tokenAuthenticator(GiteaPagesInfo{a.Gitea, a.Pages}),
	// 	db.UserSessionFromToken, db.UserFromUserSession,
	// 	authdClient,
	// ).Get("/_register_webhook", func(w http.ResponseWriter, r *http.Request) {
	// 	user, _ := database.UserFromContext(r.Context())
	// 	client := r.Context().Value(clientCtxKey{}).(*gitea.Client)
	// 	hooks, err := allGiteaPages(r.Context(), func(_ context.Context, opts gitea.ListOptions) ([]*gitea.Hook, *gitea.Response, error) {
	// 		hooks, resp, err := client.ListMyHooks(gitea.ListHooksOptions{
	// 			ListOptions: opts,
	// 		})
	// 		for _, hook := range hooks {
	// 			if hook.URL == a.Gitea.HookURL() {
	// 				return []*gitea.Hook{hook}, resp, nil
	// 			}
	// 		}
	// 		return []*gitea.Hook{}, resp, err
	// 	})
	// 	if err != nil {
	// 		slog.Error("failed to get hooks", "user", user.GiteaUID, "err", err)
	// 	}
	// 	giteaWebhookClaims := map[string]any{
	// 		a.Auth.CookieName:          int64(user.GiteaUID),
	// 		consts.ThisIsAGiteaWebhook: true,
	// 	}
	// 	_, giteaWebhookTokenString, err := a.Auth.State.tokenAuth.Encode(giteaWebhookClaims)

	// 	if len(hooks) == 0 {
	// 		slog.Warn("no gitea hook found, creating one")
	// 		hook, _, err := client.CreateMyHook(gitea.CreateHookOption{
	// 			Type:                gitea.HookTypeGitea,
	// 			AuthorizationHeader: giteaWebhookTokenString,
	// 			Config: map[string]string{
	// 				"url":          a.Gitea.HookURL(),
	// 				"content_type": "json",
	// 				"secret":       a.Gitea.HookSecret,
	// 			},
	// 			Events: []string{
	// 				"package",
	// 			},
	// 			BranchFilter: "*",
	// 			Active:       true,
	// 		})
	// 		if err != nil {
	// 			slog.Error("failed to create gitea hook", "user", user.GiteaUID, "err", err)
	// 			return
	// 		}
	// 		slog.Info("created gitea hook", "hook", hook)
	// 	} else {

	// 	}

	// })

	r.Route("/_auth", a.Auth.State.routes)

	r.Get("/{owner:^[^_].*}/{repo:^[^_].*}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusTemporaryRedirect)
	})
	r.With(
		a.Auth.State.oauthStateVerrifier,
		tokenAuthenticator(GiteaPagesInfo{a.Gitea, a.Pages}),
		db.UserSessionFromToken, db.UserFromUserSession,
		authdClient,
	).Get("/{owner:^[^_].*}/{repo:^[^_].*}/*", func(w http.ResponseWriter, r *http.Request) {
		owner := chi.URLParam(r, "owner")
		repoName := chi.URLParam(r, "repo")
		path := chi.URLParam(r, "*")
		client := r.Context().Value(clientCtxKey{}).(*gitea.Client)
		slog.Info("main endpoint hit", "owner", owner, "repo", repoName, "path", path)
		_, _, err := client.GetRepo(owner, repoName)
		if err != nil {
			slog.Error("failed to get repo info", "err", err)
			loginRequired(GiteaPagesInfo{a.Gitea, a.Pages}, w, r)
			return
		}

		// client has access to the repo, let's check if we have the page
		// input := r.Context().Value(httpin.Input).(*types.RepoVersion)
		data, fetched, err := requestPageData(&types.RepoVersion{
			Repo:    types.Repo{Owner: owner, Repo: repoName},
			File:    path,
			Version: "",
		}, db, q)
		if err != nil {
			slog.Error("failed to get page data", "err", err)
			loginRequired(GiteaPagesInfo{a.Gitea, a.Pages}, w, r)
			return
		}
		if !fetched {
			slog.Error("page not found")
			preparationPage(GiteaPagesInfo{a.Gitea, a.Pages}, w)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))
		_, err = w.Write(data)
		if err != nil {
			slog.Error("failed to write data", "err", err)
			return
		}
	})

	slog.Info("starting server", "addr", a.Server.Addr)
	server := &http.Server{Addr: a.Server.Addr, Handler: r, BaseContext: func(net.Listener) context.Context { return ctx.Context }}
	return server.ListenAndServe()
}

//go:embed index.html
var index string
var indexTemplate = template.Must(template.New("index").Parse(index))

func indexPage(gi GiteaPagesInfo, w http.ResponseWriter) {
	if err := indexTemplate.Execute(w, struct {
		Info     GiteaPagesInfo
		Redirect string
	}{
		Info: gi,
	}); err != nil {
		slog.Error("failed to execute index template", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

//go:embed login.html
var login string
var loginTemplate = template.Must(template.New("login").Parse(login))

func loginRequired(gi GiteaPagesInfo, w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Path
	if err := loginTemplate.Execute(w, struct {
		Info     GiteaPagesInfo
		Redirect string
	}{
		Info:     gi,
		Redirect: reqPath,
	}); err != nil {
		slog.Error("failed to execute login template", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

//go:embed preparation.html
var preparation string
var preparationTemplate = template.Must(template.New("preparation").Parse(preparation))

func preparationPage(gi GiteaPagesInfo, w http.ResponseWriter) {
	if err := preparationTemplate.Execute(w, struct {
		Info GiteaPagesInfo
	}{
		Info: gi,
	}); err != nil {
		slog.Error("failed to execute preparation template", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func tokenAuthenticator(gi GiteaPagesInfo) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, _, err := jwtauth.FromContext(r.Context())
			if err != nil {
				slog.Error("failed to get token from context", "err", err)
				loginRequired(gi, w, r)
				return
			}

			if token == nil {
				slog.Error("token is nil")
				loginRequired(gi, w, r)
				return
			}

			// Token is authenticated, pass it through
			next.ServeHTTP(w, r)
		})
	}
}

type clientCtxKey struct{}

func authenticatedGiteaClient(url string, oauthConfig *oauth2.Config, db *database.Database, gi GiteaPagesInfo) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := database.UserFromContext(r.Context())
			if err != nil {
				slog.Error("failed to get user from context", "err", err)
				loginRequired(gi, w, r)
				return
			}
			if user == (types.User{}) {
				slog.Error("user is nil")
				loginRequired(gi, w, r)
				return
			}

			nt, err := oauthConfig.TokenSource(r.Context(), user.Token).Token()
			if err != nil {
				slog.Error("failed to get new token", "err", err)
				loginRequired(gi, w, r)
				return
			}
			if nt != user.Token {
				slog.Warn("token updated", "old", user.Token, "new", nt)
				user.Token = nt
				err = db.Users().Set(user.GiteaUID, user)
				if err != nil {
					slog.Error("failed to update user", "err", err)
					loginRequired(gi, w, r)
					return
				}
			}

			c, err := gitea.NewClient(url, gitea.SetHTTPClient(oauthConfig.Client(r.Context(), user.Token)))
			if err != nil {
				slog.Error("failed to create gitea client", "err", err)
				loginRequired(gi, w, r)
				return
			}

			r = r.WithContext(context.WithValue(r.Context(), clientCtxKey{}, c))
			next.ServeHTTP(w, r)
		})
	}
}

// func authorizedWebhook(key string) func(next http.Handler) http.Handler {
// 	return func(next http.Handler) http.Handler {
// 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 			_, claims, err := jwtauth.FromContext(r.Context())
// 			if err != nil {
// 				slog.Error("failed to get token from context", "err", err)
// 				http.Error(w, "Unauthorized", http.StatusUnauthorized)
// 				return
// 			}
// 			if claims == nil {
// 				slog.Error("claims is nil")
// 				http.Error(w, "Unauthorized", http.StatusUnauthorized)
// 				return
// 			}
// 			maybeCheck, exists := claims[consts.ThisIsAGiteaWebhook]
// 			if !exists {
// 				slog.Error("this is a gitea webhook claim is not present")
// 				http.Error(w, "Unauthorized", http.StatusUnauthorized)
// 				return
// 			}
// 			check, exists := maybeCheck.(bool)
// 			if !exists || !check {
// 				slog.Error("this is a gitea webhook claim is wrong")
// 				http.Error(w, "Unauthorized", http.StatusUnauthorized)
// 				return
// 			}

// 			maybeUID, exists := claims[key]
// 			if !exists {
// 				slog.Error("uid webhook claim is not present")
// 				http.Error(w, "Unauthorized", http.StatusUnauthorized)
// 				return
// 			}
// 			if _, exists = maybeUID.(int64); !exists {
// 				slog.Error("uid claim is wrong")
// 				http.Error(w, "Unauthorized", http.StatusUnauthorized)
// 				return
// 			}
// 			next.ServeHTTP(w, r)
// 		})
// 	}
// }

func main() {
	err := ccli.UpdateApp(clive.Build(&app{})).Run(os.Args)
	if err != nil {
		slog.Error("Failed to run app", "err", err)
		os.Exit(1)
	}
}
