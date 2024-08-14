# pages-server

[![CI](https://github.com/ASMfreaK/pages-server/actions/workflows/ci.yml/badge.svg)](https://github.com/ASMfreaK/pages-server/actions/workflows/ci.yml)

`pages-server` is a simple server that serves pages from Gitea repositories.

It expects that for every repository there is corresponding Gitea package with a zipped pages site.
E.g. for `gitea.com/owner/repo` there should be a generic package repository `repo` in `gitea.com/owner` with a file called `docs.zip` containing the site.

This server will **not** build any content on its own. It will only serve existing code.


## Pages flow

1. Every valid Gitea user is authenticated with Gitea using OAuth2 (configured using `AUTH_GITEA_OAUTH_CLIENT_ID` and `AUTH_GITEA_OAUTH_CLIENT_SECRET`).
1. `pages-server` uses the users' oauth token to check if the user has access to the repository.
1. If the user has access to the repository, `pages-server` fetches the latest version of the repository using `GITEA_ADMIN_TOKEN` and caches it in the bbolt database.
1. `pages-server` serves the pages from the bbolt database.

### Updates

To update contents of a repo send a `POST` request to `/_update/owner/repo`.

## Usage

```
pages-server dev
pages-server simple pages server for small-to-medium gitea installations

USAGE:
    pages-server [global options] [arguments...]

GLOBAL OPTIONS:
    --pages-url value                       url for pages server (default: "http://localhost:8000") [$PAGES_URL]
    --pages-title value                     title for pages server (default: "Gitea Pages") [$PAGES_TITLE]
    --gitea-url value                       url for Gitea (default: "http://localhost:3000") [$GITEA_URL]
    --gitea-admin-token value               admin token for Gitea [$GITEA_ADMIN_TOKEN]
    --gitea-hook-secret value               secret for gitea webhooks [$GITEA_HOOK_SECRET]
    --gitea-pages-addr-from-gitea value     url for pages server as viewed from gitea (default: "http://localhost:8000") [$GITEA_PAGES_ADDR_FROM_GITEA]
    --database-filename value               path to database (default: "pages-server.db") [$DATABASE_FILENAME]
    --auth-cookie-name value                name of cookie for oauth state (default: "__i_love_pages_server") [$AUTH_COOKIE_NAME]
    --auth-secret value                     secret for auth (default: "CHANGEME") [$AUTH_SECRET]
    --auth-gitea-oauth-client-id value      oauth2 app client id from Gitea [$AUTH_GITEA_OAUTH_CLIENT_ID]
    --auth-gitea-oauth-client-secret value  oauth2 app client secret from Gitea [$AUTH_GITEA_OAUTH_CLIENT_SECRET]
    --server-addr value                     address to listen on (default: "localhost:8000") [$SERVER_ADDR]
    --help, -h                              show help
    --version, -v                           print the version
```
