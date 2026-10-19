package gateway

import (
	"context"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/proto"
)

var createdMethods = map[string]bool{
	"/rookery.v1.VMService/CreateVM":                         true,
	"/rookery.v1.DiskService/CreateDisk":                     true,
	"/rookery.v1.MediaService/CreateMedia":                   true,
	"/rookery.v1.NetworkService/CreateNetwork":               true,
	"/rookery.v1.InterfaceService/CreateInterface":           true,
	"/rookery.v1.ACLService/CreateACL":                       true,
	"/rookery.v1.VNFService/CreateVNF":                       true,
	"/rookery.v1.StorageBackendService/CreateStorageBackend": true,
	"/rookery.v1.StorageBackendService/ImportDisks":          true,
	"/rookery.v1.CaptureService/StartCapture":                true,
}

var noContentMethods = map[string]bool{
	"/rookery.v1.VMService/DeleteVM":                         true,
	"/rookery.v1.DiskService/DeleteDisk":                     true,
	"/rookery.v1.MediaService/DeleteMedia":                   true,
	"/rookery.v1.NetworkService/DeleteNetwork":               true,
	"/rookery.v1.InterfaceService/DeleteInterface":           true,
	"/rookery.v1.ACLService/DeleteACL":                       true,
	"/rookery.v1.VNFService/DeleteVNF":                       true,
	"/rookery.v1.StorageBackendService/DeleteStorageBackend": true,
	"/rookery.v1.CaptureService/DeleteCaptureFile":           true,
}

func httpResponseStatus(ctx context.Context, writer http.ResponseWriter, response proto.Message) error {
	if response == nil {
		return nil
	}
	method, ok := runtime.RPCMethod(ctx)
	if !ok {
		return nil
	}
	switch {
	case createdMethods[method]:
		writer.WriteHeader(http.StatusCreated)
	case noContentMethods[method]:
		writer.WriteHeader(http.StatusNoContent)
	}
	return nil
}
