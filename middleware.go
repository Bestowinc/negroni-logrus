package negronilogrus

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/negroni"
)

type timer interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type realClock struct{}


func (rc *realClock) Now() time.Time {
	return time.Now()
}

func (rc *realClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

// Middleware is a middleware handler that logs the request as it goes in and the response as it goes out.
type Middleware struct {
	// Logger is the log.Logger instance used to log messages with the Logger middleware
	Logger *logrus.Logger
	// Name is the name of the application as recorded in latency metrics
	Name   string
	Before func(*logrus.Entry, *http.Request, string) *logrus.Entry
	After  func(*logrus.Entry, negroni.ResponseWriter, time.Duration, string) *logrus.Entry

	logStarting bool

	clock timer

	// Exclude URLs from logging
	excludeURLs []string
}

// NewMiddleware returns a new *Middleware, yay!
func NewMiddleware() *Middleware {
	return NewCustomMiddleware(logrus.InfoLevel, &logrus.TextFormatter{}, "web")
}

// NewCustomMiddleware builds a *Middleware with the given level and formatter
func NewCustomMiddleware(level logrus.Level, formatter logrus.Formatter, name string) *Middleware {
	log := logrus.New()
	log.Level = level
	log.Formatter = formatter

	return &Middleware{
		Logger: log,
		Name:   name,
		Before: DefaultBefore,
		After:  DefaultAfter,

		logStarting: true,
		clock:       &realClock{},
	}
}

// NewMiddlewareFromLogger returns a new *Middleware which writes to a given logrus logger.
func NewMiddlewareFromLogger(logger *logrus.Logger, name string) *Middleware {
	return &Middleware{
		Logger: logger,
		Name:   name,
		Before: DefaultBefore,
		After:  DefaultAfter,

		logStarting: true,
		clock:       &realClock{},
	}
}

// SetLogStarting accepts a bool to control the logging of "started handling
// request" prior to passing to the next middleware
func (m *Middleware) SetLogStarting(v bool) {
	m.logStarting = v
}

// ExcludeURL adds a new URL u to be ignored during logging. The URL u is parsed, hence the returned error
func (m *Middleware) ExcludeURL(u string) error {
	if _, err := url.Parse(u); err != nil {
		return err
	}
	m.excludeURLs = append(m.excludeURLs, u)
	return nil
}

// ExcludedURLs returns the list of excluded URLs for this middleware
func (m *Middleware) ExcludedURLs() []string {
	return m.excludeURLs
}

func (m *Middleware) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if m.Before == nil {
		m.Before = DefaultBefore
	}

	if m.After == nil {
		m.After = DefaultAfter
	}

	for _, u := range m.excludeURLs {
		if r.URL.Path == u {
			next(rw, r)
			return
		}
	}

	start := m.clock.Now()

	// Try to get the real IP
	remoteAddr := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		remoteAddr = realIP
	}

	entry := logrus.NewEntry(m.Logger)

	if reqID := r.Header.Get("X-Request-Id"); reqID != "" {
		entry = entry.WithField("request_id", reqID)
	}

	entry = m.Before(entry, r, remoteAddr)

	if m.logStarting {
		entry.Info("started handling request")
	}

	newCtx := ToContext(r.Context(), entry)
	next(rw, r.WithContext(newCtx))

	latency := m.clock.Since(start)
	res, ok := rw.(negroni.ResponseWriter)
	if !ok {
		//ugly hack that will prevent us from merging our changes to the upstream repo!
		//unfortunately net/http does not come with same intercepting mechanism grpc package offers
		//so most HTTP handlers use a technique that wraps ResponseWriter with a private structure
		//to intercept some metrics about the request. For example, there is no way to get the response status code
		//from the built in ResponseWriter interface so one would need to wrap it as explained here:
		//https://www.reddit.com/r/golang/comments/7p35s4/how_do_i_get_the_response_status_for_my_middleware/
		//Unfortunately again, there are as many wrappers as HTTP handlers in the chain and we are at their mercy to
		//expose the data we need or the original object it wraps...
		//Our problem is that we are using OpenCensus HTTP Handler to instrument our HTTP server with OpenCensus
		//and it is f***g dumb! as everyone else it wraps ResponseWriter with a private struct
		//and it provides no public interface to cast...
		//So the work around I came up with involves putting the original ResponseWriter
		//(which happens to be negroni.ResponseWriter) on the request context before calling OpenCensus handler
		//here we fall back and read it from the context
		rw  = ExtractWriter(r.Context())
		res, ok = rw.(negroni.ResponseWriter)
	}
	if ok {
		// re-extract logger from newCtx, as it may have extra fields that changed in the holder.
		log := Extract(newCtx)
		m.After(log, res, latency, m.Name).Info("completed handling request")
	}
}

// BeforeFunc is the func type used to modify or replace the *logrus.Entry prior
// to calling the next func in the middleware chain
type BeforeFunc func(*logrus.Entry, *http.Request, string) *logrus.Entry

// AfterFunc is the func type used to modify or replace the *logrus.Entry after
// calling the next func in the middleware chain
type AfterFunc func(*logrus.Entry, negroni.ResponseWriter, time.Duration, string) *logrus.Entry

// DefaultBefore is the default func assigned to *Middleware.Before
func DefaultBefore(entry *logrus.Entry, req *http.Request, remoteAddr string) *logrus.Entry {
	return entry.WithFields(logrus.Fields{
		"request": req.RequestURI,
		"method":  req.Method,
		"remote":  remoteAddr,
	})
}

// DefaultAfter is the default func assigned to *Middleware.After
func DefaultAfter(entry *logrus.Entry, res negroni.ResponseWriter, latency time.Duration, name string) *logrus.Entry {
	return entry.WithFields(logrus.Fields{
		"status":      res.Status(),
		"text_status": http.StatusText(res.Status()),
		"took":        latency,
		fmt.Sprintf("measure#%s.latency", name): latency.Nanoseconds(),
	})
}
