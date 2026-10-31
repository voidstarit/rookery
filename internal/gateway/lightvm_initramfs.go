package gateway

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/store"
)

type lightVMInitramfsService struct {
	rookeryv1.UnimplementedLightVMInitramfsServiceServer
	store      *store.Store
	storageDir string
}

// ListInitramfs returns every initramfs record ordered by name.
func (s *lightVMInitramfsService) ListInitramfs(ctx context.Context, _ *rookeryv1.ListInitramfsRequest) (*rookeryv1.ListInitramfsResponse, error) {
	images, err := s.store.ListLightVMInitramfs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list initramfs images")
	}
	response := &rookeryv1.ListInitramfsResponse{Initramfs: make([]*rookeryv1.LightVMInitramfs, 0, len(images))}
	for _, image := range images {
		response.Initramfs = append(response.Initramfs, lightVMInitramfsToProto(image))
	}
	return response, nil
}

// GetInitramfs returns one initramfs record.
func (s *lightVMInitramfsService) GetInitramfs(ctx context.Context, request *rookeryv1.GetInitramfsRequest) (*rookeryv1.LightVMInitramfs, error) {
	image, err := s.store.GetLightVMInitramfs(ctx, request.GetId())
	if err != nil {
		return nil, lightVMInitramfsError(err, "initramfs not found")
	}
	return lightVMInitramfsToProto(image), nil
}

// CreateInitramfs registers an uploaded or existing initramfs file.
func (s *lightVMInitramfsService) CreateInitramfs(ctx context.Context, request *rookeryv1.CreateInitramfsRequest) (*rookeryv1.LightVMInitramfs, error) {
	image, err := s.initramfsFromRequest(0, request.GetName(), request.GetDescription(), request.GetPath(), request.GetFileName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	created, err := s.store.CreateLightVMInitramfs(ctx, image)
	if err != nil {
		return nil, lightVMInitramfsError(err, "initramfs not found")
	}
	return lightVMInitramfsToProto(created), nil
}

// UpdateInitramfs changes the editable fields of an initramfs record and
// cleans up a replaced managed file after the database update succeeds.
func (s *lightVMInitramfsService) UpdateInitramfs(ctx context.Context, request *rookeryv1.UpdateInitramfsRequest) (*rookeryv1.LightVMInitramfs, error) {
	existing, err := s.store.GetLightVMInitramfs(ctx, request.GetId())
	if err != nil {
		return nil, lightVMInitramfsError(err, "initramfs not found")
	}
	image, err := s.initramfsFromRequest(existing.ID, request.GetName(), request.GetDescription(), request.GetPath(), request.GetFileName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	updated, err := s.store.UpdateLightVMInitramfs(ctx, image)
	if err != nil {
		return nil, lightVMInitramfsError(err, "initramfs not found")
	}
	if existing.Managed && existing.Path != updated.Path {
		if err := os.Remove(existing.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Err(err).Str("path", existing.Path).Msg("could not remove replaced initramfs file")
		}
	}
	return lightVMInitramfsToProto(updated), nil
}

// DeleteInitramfs removes an initramfs record and its managed file.
func (s *lightVMInitramfsService) DeleteInitramfs(ctx context.Context, request *rookeryv1.DeleteInitramfsRequest) (*emptypb.Empty, error) {
	image, err := s.store.DeleteLightVMInitramfs(ctx, request.GetId())
	if err != nil {
		return nil, lightVMInitramfsError(err, "initramfs not found")
	}
	if image.Managed {
		if err := os.Remove(image.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Err(err).Str("path", image.Path).Msg("could not remove deleted initramfs file")
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *lightVMInitramfsService) initramfsFromRequest(id int64, name, description, path, fileName string) (store.LightVMInitramfs, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.LightVMInitramfs{}, errors.New("name is required")
	}
	description = strings.TrimSpace(description)
	if len(description) > 2000 {
		return store.LightVMInitramfs{}, errors.New("description must be at most 2000 characters")
	}

	path, size, err := inspectLightVMFile(path, "initramfs")
	if err != nil {
		return store.LightVMInitramfs{}, err
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		fileName = filepath.Base(path)
	}
	if fileName == "." || fileName == "/" {
		fileName = filepath.Base(path)
	}

	return store.LightVMInitramfs{
		ID:          id,
		Name:        name,
		Description: description,
		Path:        path,
		FileName:    fileName,
		SizeBytes:   size,
		Managed:     lightvm.IsManaged(s.storageDir, path),
	}, nil
}

func lightVMInitramfsToProto(image store.LightVMInitramfs) *rookeryv1.LightVMInitramfs {
	return &rookeryv1.LightVMInitramfs{
		Id:          image.ID,
		Name:        image.Name,
		Description: image.Description,
		Path:        image.Path,
		FileName:    image.FileName,
		SizeBytes:   image.SizeBytes,
		Managed:     image.Managed,
		CreatedAt:   timestamp(image.CreatedAt),
		UpdatedAt:   timestamp(image.UpdatedAt),
	}
}

func lightVMInitramfsError(err error, notFoundMessage string) error {
	switch {
	case errors.Is(err, store.ErrLightVMInitramfsNotFound):
		return status.Error(codes.NotFound, notFoundMessage)
	case errors.Is(err, store.ErrLightVMInitramfsNameTaken):
		return status.Error(codes.AlreadyExists, "an initramfs with that name already exists")
	case errors.Is(err, store.ErrLightVMInitramfsInUse):
		return status.Error(codes.Aborted, "initramfs is used by a LightVM; remove or update the LightVM first")
	default:
		return status.Error(codes.Internal, "could not save initramfs")
	}
}
