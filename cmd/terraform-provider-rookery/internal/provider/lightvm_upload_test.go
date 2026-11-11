package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

type fakeArtifactRPCClient struct {
	stream    *fakeUploadClientStream
	uploadErr error
	deleteErr error
}

func (f *fakeArtifactRPCClient) UploadArtifact(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[rookeryv1.UploadArtifactRequest, rookeryv1.UploadArtifactResponse], error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	return f.stream, nil
}

func (f *fakeArtifactRPCClient) DeleteArtifact(context.Context, *rookeryv1.DeleteArtifactRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &emptypb.Empty{}, nil
}

type fakeUploadClientStream struct {
	sends     int
	sendErrAt int
	sendErr   error
	closeErr  error
}

func (f *fakeUploadClientStream) Send(*rookeryv1.UploadArtifactRequest) error {
	f.sends++
	if f.sendErrAt > 0 && f.sends >= f.sendErrAt {
		return f.sendErr
	}
	return nil
}
func (f *fakeUploadClientStream) CloseAndRecv() (*rookeryv1.UploadArtifactResponse, error) {
	if f.closeErr != nil {
		return nil, f.closeErr
	}
	return &rookeryv1.UploadArtifactResponse{Path: "/managed/file"}, nil
}
func (f *fakeUploadClientStream) Header() (metadata.MD, error) { return nil, nil }
func (f *fakeUploadClientStream) Trailer() metadata.MD         { return nil }
func (f *fakeUploadClientStream) CloseSend() error             { return nil }
func (f *fakeUploadClientStream) Context() context.Context     { return context.Background() }
func (f *fakeUploadClientStream) SendMsg(any) error            { return nil }
func (f *fakeUploadClientStream) RecvMsg(any) error            { return nil }

func TestUploadLightVMArtifactLocalAndStreamErrors(t *testing.T) {
	ctx := context.Background()
	client := &Client{}
	if _, err := client.UploadLightVMArtifact(ctx, "kernel", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing source file was accepted")
	}
	if _, err := client.UploadLightVMArtifact(ctx, "kernel", t.TempDir()); err == nil {
		t.Fatal("directory source was accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UploadLightVMArtifact(ctx, "kernel", empty); err == nil {
		t.Fatal("empty source was accepted")
	}

	streamErr := errors.New("send failed")
	client.LightVMArtifacts = &fakeArtifactRPCClient{uploadErr: errors.New("open failed")}
	if _, err := client.UploadLightVMArtifact(ctx, "kernel", testUploadFile(t, 1)); err == nil {
		t.Fatal("stream open failure was ignored")
	}
	client.LightVMArtifacts = &fakeArtifactRPCClient{stream: &fakeUploadClientStream{sendErrAt: 1, sendErr: streamErr}}
	if _, err := client.UploadLightVMArtifact(ctx, "kernel", testUploadFile(t, 1)); err == nil {
		t.Fatal("metadata send failure was ignored")
	}
	client.LightVMArtifacts = &fakeArtifactRPCClient{stream: &fakeUploadClientStream{sendErrAt: 2, sendErr: streamErr}}
	if _, err := client.UploadLightVMArtifact(ctx, "kernel", testUploadFile(t, lightVMUploadChunkSize+1)); err == nil {
		t.Fatal("chunk send failure was ignored")
	}
	client.LightVMArtifacts = &fakeArtifactRPCClient{stream: &fakeUploadClientStream{closeErr: errors.New("close failed")}}
	if _, err := client.UploadLightVMArtifact(ctx, "kernel", testUploadFile(t, 1)); err == nil {
		t.Fatal("close failure was ignored")
	}
	client.LightVMArtifacts = &fakeArtifactRPCClient{stream: &fakeUploadClientStream{}}
	response, err := client.UploadLightVMArtifact(ctx, "kernel", testUploadFile(t, 1))
	if err != nil || response.GetPath() != "/managed/file" {
		t.Fatalf("upload = %+v, %v", response, err)
	}
	if err := client.DeleteLightVMArtifact(ctx, ""); err != nil {
		t.Fatalf("empty delete: %v", err)
	}
	client.LightVMArtifacts = &fakeArtifactRPCClient{deleteErr: errors.New("delete failed")}
	if err := client.DeleteLightVMArtifact(ctx, "/managed/file"); err == nil {
		t.Fatal("delete failure was ignored")
	}
}

func testUploadFile(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
