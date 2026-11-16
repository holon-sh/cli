//go:build kiln

package kiln

/*
#cgo pkg-config: kiln
#include <kiln.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

func plan(ctx context.Context, request any) (string, error) {
	input, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	native := C.kiln_request_create((*C.char)(unsafe.Pointer(unsafe.SliceData(input))), C.size_t(len(input)))
	if native == nil {
		return "", errors.New("cannot allocate Kiln request")
	}
	defer C.kiln_request_free(native)
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		C.kiln_request_cancel(native)
		close(canceled)
	})
	response := C.kiln_plan(native)
	if !stop() {
		<-canceled
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	output := C.GoBytes(unsafe.Pointer(response.data), C.int(response.len))
	if response.status == C.KILN_OK {
		var result struct {
			Dockerfile string `json:"dockerfile"`
		}
		err := json.Unmarshal(output, &result)
		return result.Dockerfile, err
	}
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(output, &failure) == nil && failure.Error != "" {
		return "", errors.New(failure.Error)
	}
	return "", fmt.Errorf("kiln FFI: %s", output)
}
