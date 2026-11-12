//go:build !kiln

package kiln

import "context"

const Linked = false

func Plan(context.Context, Request) (string, error) {
	return "", ErrNotLinked
}
