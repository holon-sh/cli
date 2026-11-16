//go:build !kiln

package kiln

import "context"

func plan(context.Context, any) (string, error) {
	return "", ErrNotLinked
}
