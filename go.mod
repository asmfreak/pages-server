module github.com/ASMfreaK/pages-server/pages-server

go 1.22.5

require (
	code.gitea.io/sdk/gitea v0.19.0
	github.com/ASMfreaK/clive2 v0.5.1
	github.com/bitfield/script v0.22.1
	github.com/cirruslabs/echelon v1.9.0
	github.com/fatih/color v1.17.0
	github.com/go-chi/chi/v5 v5.1.0
	github.com/go-chi/httplog/v2 v2.1.1
	github.com/go-chi/jwtauth/v5 v5.3.1
	github.com/golang-queue/queue v0.2.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/lestrrat-go/jwx/v2 v2.0.20
	github.com/oklog/ulid/v2 v2.1.0
	github.com/philippgille/gokv v0.7.0
	github.com/philippgille/gokv/encoding v0.7.0
	github.com/philippgille/gokv/syncmap v0.7.0
	github.com/philippgille/gokv/util v0.7.0
	github.com/tdewolff/minify v2.3.6+incompatible
	github.com/urfave/cli/v2 v2.27.3
	go.etcd.io/bbolt v1.3.8
	golang.org/x/sync v0.8.0
)

require (
	github.com/itchyny/gojq v0.12.13 // indirect
	github.com/itchyny/timefmt-go v0.1.5 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/tdewolff/parse v2.3.4+incompatible // indirect
	github.com/tdewolff/test v1.0.10 // indirect
	golang.org/x/text v0.14.0 // indirect
	mvdan.cc/sh/v3 v3.7.0 // indirect
)

// this is a temporary workaround for https://github.com/khepin/liteq/pull/1
replace github.com/khepin/liteq => github.com/ASMfreaK/liteq v0.1.1

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.4 // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.2.0 // indirect
	github.com/go-fed/httpsig v1.1.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-version v1.6.0 // indirect
	github.com/iancoleman/strcase v0.3.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.2 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc v1.0.4 // indirect
	github.com/lestrrat-go/iter v1.0.2 // indirect
	github.com/lestrrat-go/option v1.0.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/stretchr/testify v1.9.0 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	golang.org/x/crypto v0.22.0 // indirect
	golang.org/x/oauth2 v0.22.0
	golang.org/x/sys v0.22.0 // indirect
)
