package gateway

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/lightvm"
)

type fakeArtifactStream struct {
	requests []*rookeryv1.UploadArtifactRequest
	index    int
	recvErr  error
	response *rookeryv1.UploadArtifactResponse
}

func (f *fakeArtifactStream) Recv() (*rookeryv1.UploadArtifactRequest, error) {
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	if f.index >= len(f.requests) {
		return nil, io.EOF
	}
	request := f.requests[f.index]
	f.index++
	return request, nil
}
func (f *fakeArtifactStream) SendAndClose(response *rookeryv1.UploadArtifactResponse) error {
	f.response = response
	return nil
}
func (f *fakeArtifactStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeArtifactStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeArtifactStream) SetTrailer(metadata.MD)       {}
func (f *fakeArtifactStream) Context() context.Context     { return context.Background() }
func (f *fakeArtifactStream) SendMsg(any) error            { return nil }
func (f *fakeArtifactStream) RecvMsg(any) error            { return nil }

func TestLightVMArtifactUploadAndDelete(t *testing.T) {
	storage, err := lightvm.NewStorage(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	service := &lightVMArtifactService{storage: storage}
	stream := &fakeArtifactStream{requests: []*rookeryv1.UploadArtifactRequest{
		{Kind: lightvm.KindKernel, FileName: "vmlinuz", Chunk: []byte("hello ")},
		{Chunk: []byte("world")},
		{},
	}}
	if err := service.UploadArtifact(stream); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if stream.response == nil || stream.response.GetSizeBytes() != 11 || stream.response.GetFileName() != "vmlinuz" {
		t.Fatalf("response = %+v", stream.response)
	}
	data, err := os.ReadFile(stream.response.GetPath())
	if err != nil || string(data) != "hello world" {
		t.Fatalf("stored file = %q, %v", data, err)
	}
	if !storage.Managed(stream.response.GetPath()) {
		t.Fatal("uploaded path is not managed")
	}
	if _, err := service.DeleteArtifact(context.Background(), &rookeryv1.DeleteArtifactRequest{Path: stream.response.GetPath()}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(stream.response.GetPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still exists: %v", err)
	}

	for _, request := range []*rookeryv1.UploadArtifactRequest{
		{},
		{Kind: "other", FileName: "x"},
	} {
		bad := &fakeArtifactStream{requests: []*rookeryv1.UploadArtifactRequest{request}}
		if err := service.UploadArtifact(bad); err == nil {
			t.Fatalf("invalid upload %+v succeeded", request)
		}
	}
	empty := &fakeArtifactStream{}
	if err := service.UploadArtifact(empty); err == nil {
		t.Fatal("empty upload metadata succeeded")
	}
	if err := (&lightVMArtifactService{}).UploadArtifact(stream); err == nil {
		t.Fatal("upload without storage succeeded")
	}
	if _, err := (&lightVMArtifactService{}).DeleteArtifact(context.Background(), &rookeryv1.DeleteArtifactRequest{Path: "/tmp/x"}); err == nil {
		t.Fatal("delete without storage succeeded")
	}
	if _, err := service.DeleteArtifact(context.Background(), &rookeryv1.DeleteArtifactRequest{}); err == nil {
		t.Fatal("empty delete path succeeded")
	}
	if _, err := service.DeleteArtifact(context.Background(), &rookeryv1.DeleteArtifactRequest{Path: filepath.Join(t.TempDir(), "other")}); err == nil {
		t.Fatal("unmanaged delete path succeeded")
	}
}

func TestLightVMArtifactUploadErrors(t *testing.T) {
	storage, err := lightvm.NewStorage(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	service := &lightVMArtifactService{storage: storage}
	tooLarge := &fakeArtifactStream{requests: []*rookeryv1.UploadArtifactRequest{{Kind: lightvm.KindKernel, FileName: "big", Chunk: []byte("12")}}}
	if err := service.UploadArtifact(tooLarge); err == nil {
		t.Fatal("oversized upload succeeded")
	}
	empty := &fakeArtifactStream{requests: []*rookeryv1.UploadArtifactRequest{{Kind: lightvm.KindKernel, FileName: "empty"}}}
	if err := service.UploadArtifact(empty); err == nil {
		t.Fatal("empty upload succeeded")
	}
	streamErr := errors.New("stream failed")
	if err := service.UploadArtifact(&fakeArtifactStream{recvErr: streamErr}); err == nil {
		t.Fatal("stream read error succeeded")
	}
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	badStorage, err := lightvm.NewStorage(file, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&lightVMArtifactService{storage: badStorage}).UploadArtifact(&fakeArtifactStream{requests: []*rookeryv1.UploadArtifactRequest{{Kind: lightvm.KindKernel, Chunk: []byte("data")}}}); err == nil {
		t.Fatal("upload to a non-directory storage root succeeded")
	}
	dir := filepath.Join(storage.Dir(), "kernels", "non-empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteArtifact(context.Background(), &rookeryv1.DeleteArtifactRequest{Path: dir}); err == nil {
		t.Fatal("non-empty managed directory delete succeeded")
	}
	if err := artifactStreamError(status.Error(codes.Canceled, "canceled")); status.Code(err) != codes.Canceled {
		t.Fatalf("status error = %v", err)
	}
}

func TestArtifactStreamReader(t *testing.T) {
	stream := &fakeArtifactStream{requests: []*rookeryv1.UploadArtifactRequest{
		{Chunk: []byte("abcd")},
		{Chunk: nil},
		{Chunk: []byte("ef")},
	}}
	reader := &artifactStreamReader{stream: stream}
	buffer := make([]byte, 2)
	if n, err := reader.Read(buffer); n != 2 || err != nil || string(buffer) != "ab" {
		t.Fatalf("first read = %d, %v, %q", n, err, buffer)
	}
	if n, err := reader.Read(buffer); n != 2 || err != nil || string(buffer) != "cd" {
		t.Fatalf("second read = %d, %v, %q", n, err, buffer)
	}
	if n, err := reader.Read(buffer); n != 2 || err != nil || string(buffer) != "ef" {
		t.Fatalf("third read = %d, %v, %q", n, err, buffer)
	}
	if _, err := reader.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("final read error = %v", err)
	}
	if n, err := reader.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty read = %d, %v", n, err)
	}
	if err := artifactStreamError(io.EOF); err == nil {
		t.Fatal("empty stream error was not translated")
	}
	if err := artifactStreamError(errors.New("stream failed")); err == nil {
		t.Fatal("stream error was not translated")
	}
}
