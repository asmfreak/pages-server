package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bitfield/script"
	"github.com/cirruslabs/echelon"
	"github.com/cirruslabs/echelon/renderers"
	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/css"
	"github.com/tdewolff/minify/js"
	"golang.org/x/sync/errgroup"
)

type progressR struct {
	log       *echelon.Logger
	inner     io.Reader
	bytesRead int
}

// Read implements io.Reader.
func (pr *progressR) Read(p []byte) (n int, err error) {
	n, err = pr.inner.Read(p)
	pr.bytesRead += n
	if err != nil {
		if errors.Is(err, io.EOF) {
			pr.log.Finish(true)
		} else {
			pr.log.Infof("Error %s", err.Error())
			pr.log.Finish(false)
		}
	}
	return
}

type filterFunc func(r io.Reader, w io.Writer) error

var urlRx = regexp.MustCompile(`url\(([^)]+)\)`)

type findState struct {
	data []byte
	c    chan struct{}
}

func fontCSS(parentLog *echelon.Logger, origURL string) *script.Pipe {
	origURLDir, err := url.Parse(origURL)
	if err != nil {
		panic(err)
	}
	origURLDir.Path = path.Dir(origURLDir.Path)
	origURLDir.Path = strings.TrimSuffix(origURLDir.Path, "/")
	return script.Get(origURL).Filter(func(r io.Reader, w io.Writer) (err error) {
		log := parentLog.Scoped("Fetching " + origURL)
		bs, err := io.ReadAll(&progressR{
			inner: r,
			log:   log.Scoped("Reading url"),
		})
		if err != nil {
			return nil
		}
		eg, ctx := errgroup.WithContext(context.Background())
		eg.SetLimit(5)
		writeChan := make(chan *findState)
		closeWriteChan := sync.OnceFunc(func() {
			close(writeChan)
		})
		defer closeWriteChan()
		nparts := atomic.Int32{}
		eg.Go(func() (err error) {
			wlog := log.Scoped("Writing fetched")
			wlog.Infof("Starting")
			defer func() {
				if err != nil {
					wlog.Errorf("Error: %s", err.Error())
					wlog.Finish(false)
					return
				}
				wlog.Finish(true)
			}()
			d := ctx.Done()
			partn := 0
			for {
				wlog.Infof("Wait part %d/%d", partn, nparts.Load())
				select {
				case <-d:
					err = ctx.Err()
					if errors.Is(err, context.Canceled) {
						return
					}
					return
				case fs, ok := <-writeChan:
					if !ok {
						return
					}
					if fs.c != nil {
						wlog.Infof("Part is fetching %d/%d", partn, nparts.Load())
						select {
						case <-d:
							err = ctx.Err()
							if errors.Is(err, context.Canceled) {
								return
							}
							return
						case <-fs.c:
							wlog.Infof("Part fetched %d/%d", partn, nparts.Load())
						}
					}
					wlog.Infof("Write part %d/%d", partn, nparts.Load())
					_, err = w.Write(fs.data)
					if err != nil {
						return
					}
					partn++
				}
			}
		})
		indexes := urlRx.FindAllSubmatchIndex(bs, -1)
		lastIndex := 0
		nparts.Store(int32(2*len(indexes) + 2))
		for _, index := range indexes {
			si, ei := index[2], index[3]
			if lastIndex != si {
				writeChan <- &findState{
					data: bs[lastIndex : si-1],
					c:    nil,
				}
				lastIndex = ei + 1
			}

			urlB := bs[si:ei]
			urlS := string(bytes.TrimSpace(urlB))
			urlS = strings.TrimPrefix(urlS, "\"")
			urlS = strings.TrimSuffix(urlS, "\"")
			var urlU *url.URL
			urlU, err = url.Parse(urlS)
			if err != nil {
				return err
			}
			ext := path.Ext(urlU.Path)
			mt := mime.TypeByExtension(ext)
			if mt == "" {
				log.Errorf("failed to deduce mime type for %s (ext %s)", urlS, ext)
				err = errors.New("failed to deduce mime type")
				return
			}
			if !strings.HasPrefix(urlS, "https") {
				nu := new(url.URL)
				*nu = *origURLDir
				nu.Path = path.Join(nu.Path, urlU.Path)
				path.Clean(nu.Path)
				nu.RawQuery = urlU.RawQuery
				urlU = nu
			}
			urlS = urlU.String()
			fs := &findState{
				data: nil,
				c:    make(chan struct{}),
			}
			eg.Go(func(fs *findState) func() error {
				return func() (err error) {
					defer close(fs.c)
					flog := log.Scoped("Fetching part " + urlS)
					defer func() {
						if err != nil {
							flog.Errorf("Error: %s", err.Error())
							flog.Finish(false)
							return
						}
						flog.Finish(true)
					}()
					b, err := script.Get(urlS).Filter(func(r io.Reader, w io.Writer) (err error) {
						_, err = fmt.Fprintf(w, "data:%s;charset=utf-8;base64", mt)
						if err != nil {
							return err
						}
						enc := base64.NewEncoder(base64.StdEncoding, w)
						_, err = io.Copy(enc, &progressR{
							log:   flog.Scoped("Reading data"),
							inner: r,
						})
						if err != nil {
							return err
						}
						err = enc.Close()
						if err != nil {
							return err
						}
						return nil
					}).Bytes()
					if err != nil {
						return err
					}
					fs.data = b
					return nil
				}
			}(fs))
			writeChan <- fs
		}
		if lastIndex != len(bs)-1 {
			writeChan <- &findState{
				data: bs[lastIndex : len(bs)-1],
				c:    nil,
			}
		}
		closeWriteChan()
		err = eg.Wait()
		if err != nil {
			return err
		}
		return nil
	})
}

func minifyCSS() filterFunc {
	return func(r io.Reader, w io.Writer) error {
		min := minify.New()
		err := css.Minify(min, w, r, nil)
		if err != nil {
			return fmt.Errorf("failed to minify css: %w", err)
		}
		return nil
	}
}

const embedCSSInJS = `(function(){
	const styles = STYLE;
	try {
		const result = pako.inflate(Uint8Array.from(atob(styles), m => m.charCodeAt(0)));
		var styleSheet = document.createElement("style");
		styleSheet.textContent = new TextDecoder().decode(result);
		document.head.appendChild(styleSheet);
	} catch (err) {
		console.log(err);
	}
})();`

var embedCSSInJSPre, embedCSSInJSPost, _ = strings.Cut(embedCSSInJS, "STYLE")

func embedCSS() filterFunc {
	return func(r io.Reader, w io.Writer) error {
		_, err := w.Write([]byte(embedCSSInJSPre))
		if err != nil {
			return err
		}
		_, err = w.Write([]byte("`"))
		if err != nil {
			return err
		}
		enc := base64.NewEncoder(base64.StdEncoding, w)
		gz := gzip.NewWriter(enc)
		_, err = io.Copy(gz, r)
		if err != nil {
			return err
		}
		err = gz.Close()
		if err != nil {
			return err
		}
		err = enc.Close()
		if err != nil {
			return err
		}
		_, err = w.Write([]byte("`"))
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(embedCSSInJSPost))
		if err != nil {
			return err
		}
		return nil
	}
}

func minifyJS(log *echelon.Logger) filterFunc {
	return func(r io.Reader, w io.Writer) (err error) {
		log.Infof("Started")
		defer func() { log.Infof("Done"); log.Finish(err == nil) }()
		min := minify.New()
		err = js.Minify(min, w, r, nil)
		if err != nil {
			err = fmt.Errorf("failed to minify js: %w", err)
			return
		}
		return
	}
}

func getAndMinify(log *echelon.Logger, u string) *script.Pipe {
	scoped := log.Scoped("Fetching " + u)
	return script.Get(u).Filter(minifyJS(scoped.Scoped("Minifying JS"))).Filter(func(r io.Reader, w io.Writer) error {
		_, err := io.Copy(w, &progressR{
			log:   scoped,
			inner: r,
		})
		if err != nil {
			return err
		}
		return nil
	})
}

func main() {
	err := mime.AddExtensionType(".eot", "application/vnd.ms-fontobject")
	if err != nil {
		panic(err)
	}
	renderer := renderers.NewInteractiveRenderer(os.Stdout, nil)
	go renderer.StartDrawing()
	defer renderer.StopDrawing()
	log := echelon.NewLogger(echelon.InfoLevel, renderer)
	_, err = script.NewPipe().
		Filter(func(_ io.Reader, w io.Writer) error {
			rs := []io.ReadCloser{
				getAndMinify(log, "https://cdnjs.cloudflare.com/ajax/libs/pako/2.1.0/pako_inflate.min.js"),
				fontCSS(log, "https://fonts.googleapis.com/icon?family=Material+Icons").
					Filter(minifyCSS()).
					Filter(embedCSS()).
					Filter(minifyJS(log.Scoped("mi"))),
				fontCSS(log, "https://cdnjs.cloudflare.com/ajax/libs/materialize/0.97.5/css/materialize.min.css").
					Filter(embedCSS()).
					Filter(minifyJS(log.Scoped("matcss"))),
				getAndMinify(log, "https://cdn.jsdelivr.net/npm/darkmode-js@1.5.7/lib/darkmode-js.min.js"),
			}
			for i, r := range rs {
				log.Infof("starting copy of %dth file", i)
				_, cerr := io.Copy(w, r)
				if cerr != nil {
					return cerr
				}
			}
			return nil
		}).
		Filter(minifyJS(log.Scoped("Minify index.js"))).
		WriteFile("index.js")
	if err != nil {
		panic(err)
	}
}
