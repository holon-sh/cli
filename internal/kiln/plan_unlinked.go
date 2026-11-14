//go:build !kiln

package kiln

import "context"

func plan(context.Context, any, any) error {
	return ErrNotLinked
}
