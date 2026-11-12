//go:build !kiln

package kiln

import "context"

func Plan(context.Context, Request) (string, error) {
	return "", ErrNotLinked
}
