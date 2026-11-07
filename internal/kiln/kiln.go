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

type Request struct {
	ConfigFilePath string   `json:"configFilePath,omitempty"`
	Source         string   `json:"source"`
	Platforms      []string `json:"platforms"`
	Start          []string `json:"start,omitempty"`
}

type Result struct {
	Dockerfile string `json:"dockerfile"`
}

func Plan(ctx context.Context, request Request) (Result, error) {
	input, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	native := C.kiln_request_create((*C.char)(unsafe.Pointer(unsafe.SliceData(input))), C.size_t(len(input)))
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
	if response.status == C.KILN_OK {
		var result Result
		err = json.Unmarshal(output, &result)
		return result, err
	}
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
