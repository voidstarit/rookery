package host

import (
	"context"
	"errors"
	"fmt"
	"io"

	interfaces "go.fd.io/govpp/binapi/interface"
	"go.fd.io/govpp/binapi/interface_types"
	"go.fd.io/govpp/binapi/l2"
	"go.fd.io/govpp/binapi/pipe"
	"go.fd.io/govpp/core"
)

// PipeRuntime describes a VPP pipe pair and its bridge-domain endpoints.
type PipeRuntime struct {
	Instance           uint32
	ParentSwIfIndex    uint32
	EndpointASwIfIndex uint32
	EndpointBSwIfIndex uint32
}

// CreatePipe creates a VPP pipe and attaches its two ends to the supplied
// bridge domains.
func (v *VPPNetwork) CreatePipe(ctx context.Context, bdA, bdB uint32) (PipeRuntime, error) {
	return v.ensurePipe(ctx, 0, false, bdA, bdB)
}

// EnsurePipe finds or creates a pipe with the requested instance and attaches
// its ends to the supplied bridge domains. Unlike CreatePipe, instance 0 means
// "find or create instance 0"; it is not treated as a request to allocate a new
// instance.
func (v *VPPNetwork) EnsurePipe(ctx context.Context, instance, bdA, bdB uint32) (PipeRuntime, error) {
	return v.ensurePipe(ctx, instance, true, bdA, bdB)
}

func (v *VPPNetwork) ensurePipe(ctx context.Context, instance uint32, findInstance bool, bdA, bdB uint32) (PipeRuntime, error) {
	if bdA == 0 || bdB == 0 {
		return PipeRuntime{}, errors.New("two bridge domains are required")
	}
	if bdA == bdB {
		return PipeRuntime{}, errors.New("pipe bridge domains must differ")
	}
	conn, err := v.connect()
	if err != nil {
		return PipeRuntime{}, err
	}
	defer conn.Disconnect()

	client := pipe.NewServiceClient(conn)
	var details pipe.PipeDetails
	found := false
	if findInstance {
		details, found, err = findPipeByInstance(ctx, client, instance)
		if err != nil {
			return PipeRuntime{}, err
		}
	}
	created := false
	if !found {
		reply, err := client.PipeCreate(ctx, &pipe.PipeCreate{
			IsSpecified:  findInstance,
			UserInstance: instance,
		})
		if err != nil {
			return PipeRuntime{}, fmt.Errorf("create pipe: %w", err)
		}
		if err := checkVPPRetval("create pipe", reply.Retval); err != nil {
			return PipeRuntime{}, err
		}
		details, found, err = findPipeByParent(ctx, client, uint32(reply.SwIfIndex))
		if err != nil {
			_ = deletePipe(ctx, client, uint32(reply.SwIfIndex))
			return PipeRuntime{}, err
		}
		if !found {
			_ = deletePipe(ctx, client, uint32(reply.SwIfIndex))
			return PipeRuntime{}, errors.New("created pipe is missing from VPP pipe dump")
		}
		created = true
	}

	runtime := PipeRuntime{
		Instance:           details.Instance,
		ParentSwIfIndex:    uint32(details.SwIfIndex),
		EndpointASwIfIndex: uint32(details.PipeSwIfIndex[0]),
		EndpointBSwIfIndex: uint32(details.PipeSwIfIndex[1]),
	}
	if err := attachPipeEnd(ctx, conn, runtime.EndpointASwIfIndex, bdA); err != nil {
		if created {
			_ = deletePipe(ctx, client, runtime.ParentSwIfIndex)
		}
		return PipeRuntime{}, err
	}
	if err := attachPipeEnd(ctx, conn, runtime.EndpointBSwIfIndex, bdB); err != nil {
		if created {
			_ = deletePipe(ctx, client, runtime.ParentSwIfIndex)
		}
		return PipeRuntime{}, err
	}
	return runtime, nil
}

func attachPipeEnd(ctx context.Context, conn *core.Connection, swIfIndex, bdID uint32) error {
	if err := setInterfaceMTU(ctx, conn, swIfIndex, defaultInterfaceMTU); err != nil {
		return fmt.Errorf("set pipe end MTU: %w", err)
	}
	flags, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		return fmt.Errorf("set pipe end up: %w", err)
	}
	if err := checkVPPRetval("set pipe end up", flags.Retval); err != nil {
		return err
	}
	reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		return fmt.Errorf("attach pipe end to bridge domain: %w", err)
	}
	return checkVPPRetval("attach pipe end to bridge domain", reply.Retval)
}

// DeletePipe removes the pipe identified by its VPP instance. The stored
// parent index is not used as a fallback: VPP may have restarted and reused
// that index for an unrelated interface, so deleting by a stale index could
// remove the wrong object.
func (v *VPPNetwork) DeletePipe(ctx context.Context, instance, _ uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	client := pipe.NewServiceClient(conn)
	details, found, err := findPipeByInstance(ctx, client, instance)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return deletePipe(ctx, client, uint32(details.SwIfIndex))
}

func findPipeByInstance(ctx context.Context, client pipe.RPCService, instance uint32) (pipe.PipeDetails, bool, error) {
	return findPipeBy(ctx, client, func(details pipe.PipeDetails) bool {
		return details.Instance == instance
	})
}

func findPipeByParent(ctx context.Context, client pipe.RPCService, parent uint32) (pipe.PipeDetails, bool, error) {
	return findPipeBy(ctx, client, func(details pipe.PipeDetails) bool {
		return uint32(details.SwIfIndex) == parent
	})
}

func findPipeBy(ctx context.Context, client pipe.RPCService, match func(pipe.PipeDetails) bool) (pipe.PipeDetails, bool, error) {
	stream, err := client.PipeDump(ctx, &pipe.PipeDump{})
	if err != nil {
		return pipe.PipeDetails{}, false, fmt.Errorf("dump pipes: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return pipe.PipeDetails{}, false, nil
		}
		if err != nil {
			return pipe.PipeDetails{}, false, fmt.Errorf("read pipe dump: %w", err)
		}
		if match(*details) {
			return *details, true, nil
		}
	}
}

func deletePipe(ctx context.Context, client pipe.RPCService, parent uint32) error {
	reply, err := client.PipeDelete(ctx, &pipe.PipeDelete{SwIfIndex: interface_types.InterfaceIndex(parent)})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete pipe: %w", err)
	}
	if err := checkVPPRetval("delete pipe", reply.Retval); err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}
