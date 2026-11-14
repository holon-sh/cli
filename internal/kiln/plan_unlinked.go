//go:build !kiln

package kiln

import "context"

func Plan(context.Context, Request) (Result, error) {
	return Result{}, ErrNotLinked
}
