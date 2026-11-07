package kouja

import (
	"context"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"
	"google.golang.org/grpc"
)

type fakeAPI struct {
	create        func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error)
	getVolume     func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error)
	getCredential func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error)
	unexport      func(context.Context, *koujav1.UnexportVolumeRequest) (*koujav1.UnexportVolumeResponse, error)
	delete        func(context.Context, *koujav1.DeleteVolumeRequest) (*koujav1.DeleteVolumeResponse, error)
	getInfo       func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error)
	listClasses   func(context.Context, *koujav1.ListStorageClassesRequest) (*koujav1.ListStorageClassesResponse, error)
}

func (f *fakeAPI) CreateVolume(ctx context.Context, in *koujav1.CreateVolumeRequest, _ ...grpc.CallOption) (*koujav1.CreateVolumeResponse, error) {
	return f.create(ctx, in)
}

func (f *fakeAPI) GetVolume(ctx context.Context, in *koujav1.GetVolumeRequest, _ ...grpc.CallOption) (*koujav1.GetVolumeResponse, error) {
	return f.getVolume(ctx, in)
}

func (f *fakeAPI) GetCredential(ctx context.Context, in *koujav1.GetCredentialRequest, _ ...grpc.CallOption) (*koujav1.GetCredentialResponse, error) {
	return f.getCredential(ctx, in)
}

func (f *fakeAPI) UnexportVolume(ctx context.Context, in *koujav1.UnexportVolumeRequest, _ ...grpc.CallOption) (*koujav1.UnexportVolumeResponse, error) {
	if f.unexport == nil {
		return &koujav1.UnexportVolumeResponse{}, nil
	}
	return f.unexport(ctx, in)
}

func (f *fakeAPI) DeleteVolume(ctx context.Context, in *koujav1.DeleteVolumeRequest, _ ...grpc.CallOption) (*koujav1.DeleteVolumeResponse, error) {
	if f.delete == nil {
		return &koujav1.DeleteVolumeResponse{}, nil
	}
	return f.delete(ctx, in)
}

func (f *fakeAPI) GetInfo(ctx context.Context, in *koujav1.GetInfoRequest, _ ...grpc.CallOption) (*koujav1.GetInfoResponse, error) {
	if f.getInfo == nil {
		return &koujav1.GetInfoResponse{}, nil
	}
	return f.getInfo(ctx, in)
}

func (f *fakeAPI) ListStorageClasses(ctx context.Context, in *koujav1.ListStorageClassesRequest, _ ...grpc.CallOption) (*koujav1.ListStorageClassesResponse, error) {
	if f.listClasses == nil {
		return &koujav1.ListStorageClassesResponse{}, nil
	}
	return f.listClasses(ctx, in)
}
