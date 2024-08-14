package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/ASMfreaK/pages-server/pages-server/consts"
	"github.com/ASMfreaK/pages-server/pages-server/database"
	"github.com/ASMfreaK/pages-server/pages-server/types"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/jwtauth/v5"
	"github.com/oklog/ulid/v2"
	"golang.org/x/oauth2"
)

type AuthInfo struct {
	CookieName string `cli:"usage:'name of cookie for oauth state',default:'__i_love_pages_server'"`
	Secret     string `cli:"usage:'secret for auth',default:'CHANGEME'"`
	GiteaOauth struct {
		ClientID     string `cli:"usage:'oauth2 app client id from Gitea'"`
		ClientSecret string `cli:"usage:'oauth2 app client secret from Gitea'"`
	} `cli:"inline"`

	State struct {
		oauthStateVerrifier func(http.Handler) http.Handler
		routes              func(r chi.Router)
		oauthConfig         *oauth2.Config
		tokenAuth           *jwtauth.JWTAuth
	} `cli:"-"`
}

func (ai *AuthInfo) Initialize(pages PagesInfo, giteaInfo GiteaInfo, db *database.Database) {
	oauthConfig := &oauth2.Config{
		RedirectURL:  fmt.Sprintf("%s/_auth/callback", pages.URL),
		ClientID:     ai.GiteaOauth.ClientID,
		ClientSecret: ai.GiteaOauth.ClientSecret,
		Scopes: []string{
			"read:repository",
			"read:package",
			string(gitea.AccessTokenScopeUser),
		},
		Endpoint: oauth2.Endpoint{
			AuthURL:       fmt.Sprintf("%s/login/oauth/authorize", giteaInfo.URL),
			TokenURL:      fmt.Sprintf("%s/login/oauth/access_token", giteaInfo.URL),
			DeviceAuthURL: fmt.Sprintf("%s/login/oauth/keys", giteaInfo.URL),
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}

	ai.State.tokenAuth = jwtauth.New("HS256", []byte(ai.Secret), nil)

	ai.State.oauthStateVerrifier = jwtauth.Verify(ai.State.tokenAuth, func(r *http.Request) string {
		oauthState, err := r.Cookie(ai.CookieName)
		if err != nil {
			slog.Warn("failed to get oauthstate cookie", "err", err)
			return ""
		}
		return oauthState.Value
	})
	ai.State.routes = func(r chi.Router) {
		r.Get("/logout", func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{
				Name:     ai.CookieName,
				Value:    "deleted",
				Expires:  time.Now().Add(20 * time.Minute),
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		})
		r.With(
			ai.State.oauthStateVerrifier,
			db.UserSessionFromToken,
			db.UserFromUserSession,
		).Get("/login", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			claimsForToken := map[string]interface{}{}

			if q.Has("redirect") {
				redir := q.Get("redirect")
				slog.Info("client wants to be redirected", "redirect", redir)
				if user, err := database.UserFromContext(r.Context()); user != (types.User{}) && err == nil {
					http.Redirect(w, r, redir, http.StatusTemporaryRedirect)
					return
				}
				claimsForToken["redirect"] = redir
			}

			uid := ulid.Make()
			needsCookie := true
			_, claims, err := jwtauth.FromContext(r.Context())
			if err == nil {
				if _, ok := claims[consts.UserID]; ok {
					uid = claims[consts.UserID].(ulid.ULID)
				}
				needsCookie = false
			}
			_, stateCookie, _ := ai.State.tokenAuth.Encode(map[string]any{
				consts.UserID: uid,
			})

			if needsCookie {
				cookie := http.Cookie{
					Name:     ai.CookieName,
					Value:    stateCookie,
					Expires:  time.Now().Add(24 * time.Hour),
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				}
				http.SetCookie(w, &http.Cookie{
					Name:   ai.CookieName,
					MaxAge: -1,
				})
				http.SetCookie(w, &cookie)
			}

			// Create oauthState cookie
			claimsForToken["state"] = stateCookie
			_, tokenString, _ := ai.State.tokenAuth.Encode(claimsForToken)

			//	AuthCodeURL receive state that is a token to protect the user from CSRF attacks. You must always provide a non-empty string and
			//	validate that it matches the the state query parameter on your redirect callback.

			u := oauthConfig.AuthCodeURL(tokenString)
			http.Redirect(w, r, u, http.StatusTemporaryRedirect)
		})
		r.With(ai.State.oauthStateVerrifier).Get("/callback", func(w http.ResponseWriter, r *http.Request) {
			_, claims, err := jwtauth.FromContext(r.Context())
			if err != nil {
				slog.Error("failed to verify state from cookie", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}

			oauthStateToken, err := jwtauth.VerifyToken(ai.State.tokenAuth, r.FormValue("state"))
			if err != nil {
				slog.Error("failed to verify oauthstate token from server", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}
			authStateClaims, err := oauthStateToken.AsMap(context.Background())
			if err != nil {
				slog.Error("failed to get oauthstate claims", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}
			maybeCookieToken, ok := authStateClaims["state"]
			if !ok {
				slog.Error("oauthState does not contain state cookie")
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}
			cookieTokenString, ok := maybeCookieToken.(string)
			if !ok {
				slog.Error("oauthState state cookie is not a string")
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}

			cookieToken, err := jwtauth.VerifyToken(ai.State.tokenAuth, cookieTokenString)
			if err != nil {
				slog.Error("failed to verify oauthState state cookie", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}
			cookieTokenClaims, err := cookieToken.AsMap(context.Background())
			if err != nil {
				slog.Error("failed to get oauthstate claims", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}

			if !reflect.DeepEqual(cookieTokenClaims, claims) {
				slog.Error("invalid oauth state", "cookieTokenClaims", cookieTokenClaims, "claims", claims)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}

			oauthToken, err := oauthConfig.Exchange(context.Background(), r.FormValue("code"))
			if err != nil {
				slog.Error("failed to exchange oauth token", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}
			giteaClient, err := gitea.NewClient(giteaInfo.URL, gitea.SetHTTPClient(oauthConfig.Client(r.Context(), oauthToken)))
			if err != nil {
				slog.Error("failed to create gitea client", "err", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			user, _, err := giteaClient.GetMyUserInfo()
			if err != nil {
				slog.Error("failed to get current user", "err", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			slog.Info("current user", "user", user)

			c := claims[consts.UserID]
			sessionToken := c.(ulid.ULID)
			uid := types.GiteaUID(user.ID)
			err = db.UserSessions().Set(sessionToken, types.UserSession{
				GiteaUID: uid,
			})
			if err != nil {
				slog.Error("failed to set user session", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}
			pagesUser, _, err := db.Users().Get(uid)
			if err != nil {
				slog.Error("failed to get user", "err", err)
			}
			pagesUser.GiteaUID = uid
			pagesUser.Token = oauthToken
			err = db.Users().Set(uid, pagesUser)
			if err != nil {
				slog.Error("failed to set user", "err", err)
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
				return
			}

			if redirect, ok := authStateClaims["redirect"]; ok {
				if redirectString, ok := redirect.(string); ok {
					http.Redirect(w, r, redirectString, http.StatusTemporaryRedirect)
					return
				}
			}
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		})
	}
}
