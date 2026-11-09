//go:build !kiln

package kiln

import "context"

const Linked = false

func Plan(context.Context, Request) (Result, error) {
	return Result{}, ErrNotLinked
}
