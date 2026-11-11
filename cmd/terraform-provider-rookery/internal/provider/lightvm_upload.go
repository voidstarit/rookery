package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

const lightVMUploadChunkSize = 1 << 20

// UploadLightVMArtifact streams a local kernel or initramfs file to managed
// storage on the Rookery host over the mTLS gRPC endpoint.
func (c *Client) UploadLightVMArtifact(ctx context.Context, kind, filePath string) (*rookeryv1.UploadArtifactResponse, error) {
	filePath = filepath.Clean(filePath)
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filePath, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", filePath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filePath)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("%s is empty", filePath)
	}

	uploadCtx, cancel := context.WithTimeout(ctx, c.UploadTimeout())
	defer cancel()
	stream, err := c.LightVMArtifacts.UploadArtifact(uploadCtx)
	if err != nil {
		return nil, fmt.Errorf("open upload stream: %w", err)
	}
	buffer := make([]byte, lightVMUploadChunkSize)
	read, readErr := file.Read(buffer)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, fmt.Errorf("read %s: %w", filePath, readErr)
	}
	first := &rookeryv1.UploadArtifactRequest{
		Kind:     kind,
		FileName: filepath.Base(filePath),
	}
	if read > 0 {
		first.Chunk = append([]byte(nil), buffer[:read]...)
	}
	if err := stream.Send(first); err != nil {
		return nil, fmt.Errorf("send upload metadata: %w", err)
	}
	for {
		read, readErr = file.Read(buffer)
		if read > 0 {
			if err := stream.Send(&rookeryv1.UploadArtifactRequest{Chunk: append([]byte(nil), buffer[:read]...)}); err != nil {
				return nil, fmt.Errorf("send upload chunk: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", filePath, readErr)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return nil, fmt.Errorf("upload %s: %w", filePath, err)
	}
	return response, nil
}

// DeleteLightVMArtifact removes a managed file created by UploadLightVMArtifact.
func (c *Client) DeleteLightVMArtifact(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, c.Timeout())
	defer cancel()
	_, err := c.LightVMArtifacts.DeleteArtifact(callCtx, &rookeryv1.DeleteArtifactRequest{Path: path})
	return err
}
