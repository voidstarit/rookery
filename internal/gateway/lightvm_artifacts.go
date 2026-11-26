package gateway

import (
	"context"
	"errors"
	"io"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/lightvm"
)

// lightVMArtifactService exposes managed LightVM artifact uploads over gRPC so
// clients using the public mTLS endpoint can upload kernel and initramfs files.
type lightVMArtifactService struct {
	rookeryv1.UnimplementedLightVMArtifactServiceServer
	storage *lightvm.Storage
}

// UploadArtifact consumes a client-streamed kernel or initramfs upload, where the first message
// carries the kind and file name, and saves it to managed LightVM storage subject to the size
// limit. It returns the stored path, file name, and size, or InvalidArgument for bad metadata and
// FailedPrecondition when storage is not configured.
func (s *lightVMArtifactService) UploadArtifact(stream rookeryv1.LightVMArtifactService_UploadArtifactServer) error {
	if s.storage == nil {
		return status.Error(codes.FailedPrecondition, "LightVM storage is not configured")
	}
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "upload metadata is required")
		}
		return artifactStreamError(err)
	}
	kind := strings.TrimSpace(first.GetKind())
	fileName := strings.TrimSpace(first.GetFileName())
	if kind != lightvm.KindKernel && kind != lightvm.KindInitramfs {
		return status.Errorf(codes.InvalidArgument, "kind must be %q or %q", lightvm.KindKernel, lightvm.KindInitramfs)
	}
	reader := &artifactStreamReader{stream: stream, pending: first.GetChunk()}
	stored, err := s.storage.Save(kind, fileName, reader)
	if err != nil {
		switch {
		case errors.Is(err, lightvm.ErrTooLarge):
			return status.Errorf(codes.InvalidArgument, "upload exceeds the %d-byte limit", s.storage.MaxUploadBytes())
		case errors.Is(err, lightvm.ErrUnsupportedKind):
			return status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, io.EOF):
			return status.Error(codes.InvalidArgument, "uploaded file is empty")
		default:
			if status.Code(err) != codes.Unknown {
				return err
			}
			return status.Errorf(codes.Internal, "store uploaded file: %v", err)
		}
	}
	return stream.SendAndClose(&rookeryv1.UploadArtifactResponse{
		Path:      stored.Path,
		FileName:  stored.FileName,
		SizeBytes: stored.SizeBytes,
	})
}

// DeleteArtifact removes a file previously uploaded to managed LightVM storage, rejecting empty
// paths and paths outside that storage; it returns an empty response on success.
func (s *lightVMArtifactService) DeleteArtifact(_ context.Context, request *rookeryv1.DeleteArtifactRequest) (*emptypb.Empty, error) {
	if s.storage == nil {
		return nil, status.Error(codes.FailedPrecondition, "LightVM storage is not configured")
	}
	target := strings.TrimSpace(request.GetPath())
	if target == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}
	if !s.storage.Managed(target) {
		return nil, status.Error(codes.InvalidArgument, "path is not in managed LightVM storage")
	}
	if err := s.storage.Remove(target); err != nil {
		return nil, status.Errorf(codes.Internal, "remove uploaded file: %v", err)
	}
	return &emptypb.Empty{}, nil
}

type artifactStreamReader struct {
	stream  rookeryv1.LightVMArtifactService_UploadArtifactServer
	pending []byte
}

// Read fills p from the buffered upload chunk, receiving further stream messages as needed;
// empty chunks are skipped and a zero-length p succeeds without consuming the stream.
func (r *artifactStreamReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		request, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		r.pending = request.GetChunk()
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

// artifactStreamError converts a stream receive failure into a gRPC error, reporting EOF as an
// empty upload, preserving an already-coded status, and otherwise returning Internal.
func artifactStreamError(err error) error {
	if errors.Is(err, io.EOF) {
		return status.Error(codes.InvalidArgument, "uploaded file is empty")
	}
	if status.Code(err) != codes.Unknown {
		return err
	}
	return status.Errorf(codes.Internal, "read upload stream: %v", err)
}
