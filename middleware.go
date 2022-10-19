package staticgzip

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

const (
	gzipEncoding    = "gzip"
	gzipExtension   = ".gz"
	brotliEncoding  = "br"
	brotliExtension = ".br"
)

type (
	// StaticConfig defines the config for Static middleware.
	StaticConfig struct {
		// Skipper defines a function to skip middleware.
		Skipper middleware.Skipper

		// Root directory from where the static content is served.
		// Required.
		Root string `yaml:"root"`

		// Index file for serving a directory.
		// Optional. Default value "index.html".
		Index string `yaml:"index"`

		// Enable HTML5 mode by forwarding all not-found requests to root so that
		// SPA (single-page application) can handle the routing.
		// Optional. Default value false.
		HTML5 bool `yaml:"html5"`

		// Whether there might be static Gzip files
		Gzip bool `yaml:"gzip"`

		// Whether there might be static Brotli files
		Brotli bool `yaml:"brotli"`

		// Enable ignoring of the base of the URL path.
		// Example: when assigning a static middleware to a non root path group,
		// the filesystem path is not doubled
		// Optional. Default value false.
		IgnoreBase bool `yaml:"ignoreBase"`

		// Filesystem provides access to the static content.
		// Optional. Defaults to http.Dir(config.Root)
		Filesystem http.FileSystem `yaml:"-"`
	}
)

var (
	// DefaultStaticConfig is the default Static middleware config.
	DefaultStaticConfig = StaticConfig{
		Skipper: middleware.DefaultSkipper,
		Index:   "index.html",
	}
)

// Middleware returns a Static middleware to serves static content from the provided
// root directory.
func Middleware(root string) echo.MiddlewareFunc {
	c := DefaultStaticConfig
	c.Root = root
	return MiddlewareWithConfig(c)
}

// MiddlewareWithConfig returns a Static middleware with config.
// See `Static()`.
func MiddlewareWithConfig(config StaticConfig) echo.MiddlewareFunc {
	// Defaults
	if config.Root == "" {
		config.Root = "." // For security we want to restrict to CWD.
	}
	if config.Skipper == nil {
		config.Skipper = DefaultStaticConfig.Skipper
	}
	if config.Index == "" {
		config.Index = DefaultStaticConfig.Index
	}
	if config.Filesystem == nil {
		config.Filesystem = http.Dir(config.Root)
		config.Root = "."
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			if config.Skipper(c) {
				return next(c)
			}

			p := c.Request().URL.Path
			if strings.HasSuffix(c.Path(), "*") { // When serving from a group, e.g. `/static*`.
				p = c.Param("*")
			}
			p, err = url.PathUnescape(p)
			if err != nil {
				return
			}
			name := filepath.Join(config.Root, filepath.Clean("/"+p)) // "/"+ for security

			if config.IgnoreBase {
				routePath := path.Base(strings.TrimRight(c.Path(), "/*"))
				baseURLPath := path.Base(p)
				if baseURLPath == routePath {
					i := strings.LastIndex(name, routePath)
					name = name[:i] + strings.Replace(name[i:], routePath, "", 1)
				}
			}

			file, err := openFile(c, config.Filesystem, name, config.Gzip, config.Brotli)
			if err != nil {
				if !os.IsNotExist(err) {
					return err
				}

				if err = next(c); err == nil {
					return err
				}

				var he *echo.HTTPError
				if !(errors.As(err, &he) && config.HTML5 && he.Code == http.StatusNotFound) {
					return err
				}

				file, err = openFile(c, config.Filesystem, filepath.Join(config.Root, config.Index), config.Gzip, config.Brotli)
				if err != nil {
					return err
				}
			}

			defer file.Close()

			info, err := file.Stat()
			if err != nil {
				return err
			}

			if info.IsDir() {
				index, err := openFile(c, config.Filesystem, filepath.Join(name, config.Index), config.Gzip, config.Brotli)
				if err != nil {
					if os.IsNotExist(err) {
						return next(c)
					}
				}

				defer index.Close()

				info, err = index.Stat()
				if err != nil {
					return err
				}

				return serveFile(c, index, info)
			}

			return serveFile(c, file, info)
		}
	}
}

func openFile(c echo.Context, fs http.FileSystem, name string, gzip bool, brotli bool) (file http.File, err error) {
	pathWithSlashes := filepath.ToSlash(name)
	acceptedEncodings := c.Request().Header.Get(echo.HeaderAcceptEncoding)

	if brotli && strings.Contains(acceptedEncodings, brotliEncoding) {
		file, err = fs.Open(pathWithSlashes + brotliExtension)

		if err == nil {
			c.Response().Header().Set(echo.HeaderContentEncoding, brotliEncoding)
			return
		}
	}

	if gzip && strings.Contains(acceptedEncodings, gzipEncoding) {
		file, err = fs.Open(pathWithSlashes + gzipExtension)

		if err == nil {
			c.Response().Header().Set(echo.HeaderContentEncoding, gzipEncoding)
			return
		}
	}

	return fs.Open(pathWithSlashes)
}

func serveFile(c echo.Context, file http.File, info os.FileInfo) error {
	http.ServeContent(c.Response(), c.Request(), info.Name(), info.ModTime(), file)
	return nil
}
