package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func (s *vnfService) writeAvailable() error {
	if s.legacy == nil {
		return status.Error(codes.Unavailable, "VNF write runtime is not configured")
	}
	return nil
}

func (s *vnfService) CreateVNF(ctx context.Context, request *rookeryv1.CreateVNFRequest) (*rookeryv1.VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	body, err := marshalRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	code, response, err := callLegacy(ctx, s.legacy, http.MethodPost, "/api/vnfs", body)
	if err != nil {
		return nil, err
	}
	if code != http.StatusCreated && code != http.StatusOK {
		return nil, legacyError(code, response)
	}
	var vnf rookeryv1.VNF
	if err := protojson.Unmarshal(response, &vnf); err != nil {
		return nil, status.Error(codes.Internal, "could not decode VNF response")
	}
	return &vnf, nil
}

func (s *vnfService) UpdateVNF(ctx context.Context, request *rookeryv1.UpdateVNFRequest) (*rookeryv1.VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	// The legacy update payload does not accept the path ID in its JSON body.
	update := proto.Clone(request).(*rookeryv1.UpdateVNFRequest)
	update.Id = 0
	body, err := marshalRequest(update)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	code, response, err := callLegacy(ctx, s.legacy, http.MethodPut, "/api/vnfs/"+strconv.FormatInt(request.GetId(), 10), body)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, legacyError(code, response)
	}
	var vnf rookeryv1.VNF
	if err := protojson.Unmarshal(response, &vnf); err != nil {
		return nil, status.Error(codes.Internal, "could not decode VNF response")
	}
	return &vnf, nil
}

func (s *vnfService) DeleteVNF(ctx context.Context, request *rookeryv1.DeleteVNFRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	code, response, err := callLegacy(ctx, s.legacy, http.MethodDelete, "/api/vnfs/"+strconv.FormatInt(request.GetId(), 10), nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusNoContent && code != http.StatusOK {
		return nil, legacyError(code, response)
	}
	return &emptypb.Empty{}, nil
}

func (s *vnfService) RestartVNF(ctx context.Context, request *rookeryv1.RestartVNFRequest) (*rookeryv1.VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	code, response, err := callLegacy(ctx, s.legacy, http.MethodPost, "/api/vnfs/"+strconv.FormatInt(request.GetId(), 10)+"/restart", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, legacyError(code, response)
	}
	var vnf rookeryv1.VNF
	if err := protojson.Unmarshal(response, &vnf); err != nil {
		return nil, status.Error(codes.Internal, "could not decode VNF response")
	}
	return &vnf, nil
}

func marshalRequest(value interface{ ProtoReflect() protoreflect.Message }) ([]byte, error) {
	return newNumericJSONMarshaler().Marshal(value)
}

func callLegacy(ctx context.Context, handler http.Handler, method, path string, body []byte) (int, []byte, error) {
	request := httptest.NewRequest(method, path, strings.NewReader(string(body))).WithContext(ctx)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes(), nil
}

func legacyError(code int, body []byte) error {
	message := strings.TrimSpace(string(body))
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error != "" {
		message = payload.Error
	}
	if message == "" {
		message = http.StatusText(code)
	}
	switch code {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return status.Error(codes.InvalidArgument, message)
	case http.StatusNotFound:
		return status.Error(codes.NotFound, message)
	case http.StatusConflict:
		return status.Error(codes.Aborted, message)
	case http.StatusServiceUnavailable:
		return status.Error(codes.Unavailable, message)
	default:
		return status.Error(codes.Internal, message)
	}
}
