package gateway

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var createdMethods = map[string]bool{
	"/rookery.v1.VMService/CreateVM":                         true,
	"/rookery.v1.DiskService/CreateDisk":                     true,
	"/rookery.v1.MediaService/CreateMedia":                   true,
	"/rookery.v1.NetworkService/CreateNetwork":               true,
	"/rookery.v1.NetworkService/CreateNetworkPipe":           true,
	"/rookery.v1.InterfaceService/CreateInterface":           true,
	"/rookery.v1.ACLService/CreateACL":                       true,
	"/rookery.v1.VNFService/CreateVNF":                       true,
	"/rookery.v1.StorageBackendService/CreateStorageBackend": true,
	"/rookery.v1.StorageBackendService/ImportDisks":          true,
	"/rookery.v1.CaptureService/StartCapture":                true,
	"/rookery.v1.RouteTableService/CreateRouteTable":         true,
	"/rookery.v1.RouteTableService/CreateRoute":              true,
	"/rookery.v1.HostService/CreateHostTap":                  true,
}

var noContentMethods = map[string]bool{
	"/rookery.v1.VMService/DeleteVM":                         true,
	"/rookery.v1.DiskService/DeleteDisk":                     true,
	"/rookery.v1.MediaService/DeleteMedia":                   true,
	"/rookery.v1.NetworkService/DeleteNetwork":               true,
	"/rookery.v1.NetworkService/DeleteNetworkPipe":           true,
	"/rookery.v1.InterfaceService/DeleteInterface":           true,
	"/rookery.v1.InterfaceService/ResetInterfaceCounters":    true,
	"/rookery.v1.ACLService/DeleteACL":                       true,
	"/rookery.v1.VNFService/DeleteVNF":                       true,
	"/rookery.v1.StorageBackendService/DeleteStorageBackend": true,
	"/rookery.v1.CaptureService/DeleteCaptureFile":           true,
	"/rookery.v1.TroubleshootService/ClearTrace":             true,
	"/rookery.v1.RouteTableService/DeleteRouteTable":         true,
	"/rookery.v1.RouteTableService/DeleteRoute":              true,
	"/rookery.v1.HostService/DeleteHostTap":                  true,
	"/rookery.v1.VMService/ResetVM":                          true,
	"/rookery.v1.VMService/CtrlAltDel":                       true,
}

func setLocationHeader(method string, response proto.Message, writer http.ResponseWriter) {
	switch value := response.(type) {
	case *rookeryv1.VM:
		writer.Header().Set("Location", "/api/vms/"+value.GetId())
	case *rookeryv1.Disk:
		writer.Header().Set("Location", fmt.Sprintf("/api/disks/%d", value.GetId()))
	case *rookeryv1.Media:
		writer.Header().Set("Location", fmt.Sprintf("/api/media/%d", value.GetId()))
	case *rookeryv1.Network:
		writer.Header().Set("Location", fmt.Sprintf("/api/networks/%d", value.GetId()))
	case *rookeryv1.NetworkPipe:
		writer.Header().Set("Location", fmt.Sprintf("/api/network-pipes/%d", value.GetId()))
	case *rookeryv1.VirtualInterface:
		writer.Header().Set("Location", fmt.Sprintf("/api/interfaces/%d", value.GetId()))
	case *rookeryv1.ACL:
		writer.Header().Set("Location", fmt.Sprintf("/api/acls/%d", value.GetId()))
	case *rookeryv1.VNF:
		writer.Header().Set("Location", fmt.Sprintf("/api/vnfs/%d", value.GetId()))
	case *rookeryv1.StorageBackend:
		writer.Header().Set("Location", fmt.Sprintf("/api/storage-backends/%d", value.GetId()))
	case *rookeryv1.RouteTable:
		writer.Header().Set("Location", fmt.Sprintf("/api/route-tables/%d", value.GetId()))
	case *rookeryv1.Route:
		writer.Header().Set("Location", fmt.Sprintf("/api/routes/%d", value.GetId()))
	case *rookeryv1.HostTap:
		writer.Header().Set("Location", fmt.Sprintf("/api/host/taps/%d", value.GetId()))
	}
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
		setLocationHeader(method, response, writer)
		writer.WriteHeader(http.StatusCreated)
	case noContentMethods[method]:
		writer.WriteHeader(http.StatusNoContent)
	}
	return nil
}
