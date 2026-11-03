package clientutil

import (
	"errors"
	"io"
)

var ErrContentTooLarge = errors.New("content exceeds the size limit")

func ReadLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrContentTooLarge
	}
	return data, nil
}
