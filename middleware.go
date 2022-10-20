package staticgzip

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
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

		// Encodings (as the header `Accept-Encoding`) in the preferred order.
		Encodings []string

		// File encodings - must match the length and order of Encodings.
		EncodingExtensions []string

		// Enable ignoring of the base of the URL path.
		// Example: when assigning a static middleware to a non root path group,
		// the filesystem path is not doubled
		// Optional. Default value false.
		IgnoreBase bool `yaml:"ignoreBase"`
	}
)

var (
	// DefaultStaticConfig is the default Static middleware config.
	DefaultStaticConfig = StaticConfig{
		Skipper:            middleware.DefaultSkipper,
		Index:              "index.html",
		Encodings:          []string{"br", "gzip"},
		EncodingExtensions: []string{".br", ".gz"},
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
	if config.Encodings == nil {
		config.Encodings = DefaultStaticConfig.Encodings
	}
	if config.EncodingExtensions == nil {
		config.EncodingExtensions = DefaultStaticConfig.EncodingExtensions
	}

	if len(config.Encodings) != len(config.EncodingExtensions) {
		panic("length of encodings and extensions must match")
	}

	fs := http.Dir(config.Root)

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

			p = filepath.Clean("/" + p) // "/"+ for security

			// Short circuit
			if p == "/" {
				p = config.Index
			}

			f, err := openFile(c, fs, p, config.Encodings, config.EncodingExtensions)

			if err != nil {

				// Any error other than "Not exists" is an error
				if !os.IsNotExist(err) {
					return echo.ErrNotFound
				}

				// Check if next route is valid - if so, return
				if err = next(c); err == nil {
					return err
				}

				// Route everything to index in SPA mode
				if config.HTML5 {
					p = config.Index
					f, err = fs.Open(p)

					if err != nil {
						return echo.ErrNotFound
					}
				} else {
					return echo.ErrNotFound
				}
			}

			info, err := f.Stat()

			if err != nil {
				return echo.ErrNotFound
			}

			if info.IsDir() {
				// Route everything to index in SPA mode
				if config.HTML5 {
					p = config.Index
					f, err = fs.Open(p)
				} else {
					p = filepath.Join(p, config.Index)
					f, err = openFile(c, fs, p, config.Encodings, config.EncodingExtensions)
				}

				if err != nil {
					return echo.ErrNotFound
				}

				info, err = f.Stat()

				if err != nil {
					return echo.ErrNotFound
				}
			}

			return serveFile(c, f, info, p)
		}
	}
}

func openFile(c echo.Context, fs http.FileSystem, p string, encodings []string, encodingExtensions []string) (file http.File, err error) {
	if acceptEncoding := c.Request().Header.Get(echo.HeaderAcceptEncoding); acceptEncoding != "" {
		for i, enc := range encodings {
			if !strings.Contains(acceptEncoding, enc) {
				continue
			}

			if file, err = fs.Open(p + encodingExtensions[i]); err == nil {
				c.Response().Header().Set(echo.HeaderContentEncoding, encodings[i])
				return
			}
		}
	}

	file, err = fs.Open(p)

	return
}

func serveFile(c echo.Context, file http.File, info os.FileInfo, name string) error {
	http.ServeContent(c.Response(), c.Request(), path.Base(name), info.ModTime(), file)
	return nil
}
