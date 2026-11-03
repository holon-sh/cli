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
	"runtime"
	"unsafe"
)

type Request struct {
	ConfigFilePath string          `json:"configFilePath,omitempty"`
	Source         string          `json:"source"`
	Platforms      []string        `json:"platforms"`
	Start          []string        `json:"start,omitempty"`
	Resolution     json.RawMessage `json:"resolution,omitempty"`
}

type Result struct {
	Dockerfile string          `json:"dockerfile"`
	Resolution json.RawMessage `json:"resolution"`
}

func Plan(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	native := C.kiln_request_create((*C.char)(unsafe.Pointer(unsafe.SliceData(input))), C.size_t(len(input)))
	runtime.KeepAlive(input)
	if native == nil {
		return Result{}, errors.New("cannot allocate Kiln request")
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
		return Result{}, err
	}
	output := C.GoBytes(unsafe.Pointer(response.data), C.int(response.len))
	switch response.status {
	case C.KILN_OK:
		var result Result
		err = json.Unmarshal(output, &result)
		return result, err
	case C.KILN_CANCELLED:
		return Result{}, context.Canceled
	default:
		var diagnostic struct {
			Logs []struct {
				Msg string `json:"Msg"`
			} `json:"logs"`
		}
		if json.Unmarshal(output, &diagnostic) == nil && len(diagnostic.Logs) > 0 {
			return Result{}, errors.New(diagnostic.Logs[0].Msg)
		}
		return Result{}, fmt.Errorf("kiln FFI: %s", output)
	}
}
