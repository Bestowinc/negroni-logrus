package negronilogrus

import (
	"context"

	"github.com/sirupsen/logrus"
)

type ctxLoggerMarker struct{}

type ctxLogger struct {
	logger *logrus.Entry
	fields logrus.Fields
}

var (
	ctxLoggerKey = &ctxLoggerMarker{}
)

// Extract takes the call-scoped logrus.Entry from ctx_logrus middleware.
//
// If the ctx_logrus middleware wasn't used, a no-op `logrus.Entry` is returned. This makes it safe to
// use regardless.
func Extract(ctx context.Context) *logrus.Entry {
	l, ok := ctx.Value(ctxLoggerKey).(*ctxLogger)
	if !ok || l == nil {
		return logrus.NewEntry(nullLogger)
	}

	fields := logrus.Fields{}

	// Add logrus fields added until now.
	for k, v := range l.fields {
		fields[k] = v
	}

	return l.logger.WithFields(fields)
}

// ToContext adds the logrus.Entry to the context for extraction later.
// Returning the new context that has been created.
func ToContext(ctx context.Context, entry *logrus.Entry) context.Context {
	l := &ctxLogger{
		logger: entry,
		fields: logrus.Fields{},
	}
	return context.WithValue(ctx, ctxLoggerKey, l)
}

