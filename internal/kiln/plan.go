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

const Linked = true

func Plan(ctx context.Context, request Request) (string, error) {
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
		err = json.Unmarshal(output, &result)
		return result.Dockerfile, err
	}
	var diagnostic struct {
		Logs []struct {
			Msg string `json:"Msg"`
		} `json:"logs"`
	}
	if json.Unmarshal(output, &diagnostic) == nil && len(diagnostic.Logs) > 0 {
		return "", errors.New(diagnostic.Logs[0].Msg)
	}
	return "", fmt.Errorf("kiln FFI: %s", output)
}
