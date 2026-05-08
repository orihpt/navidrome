// Package db is a legacy compatibility shell kept so old tests and commands do
// not pull SQLite back into Waves Music. Runtime database access lives in the
// MongoDB-backed persistence package.
package db

import (
	"context"
	"errors"
)

var ErrRemoved = errors.New("legacy SQL database package removed; Waves Music uses MongoDB only")

func Init(context.Context) func() { return func() {} }
func Optimize(context.Context)    {}
func Backup(context.Context) (string, error) {
	return "", ErrRemoved
}
func Prune(context.Context) (int, error) {
	return 0, ErrRemoved
}
func Restore(context.Context, string) error {
	return ErrRemoved
}
