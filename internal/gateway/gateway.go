// Package gateway exposes generated gRPC services through grpc-gateway.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

// Readiness supplies the current host readiness state.
type Readiness interface {
	Status() readiness.Status
}

// Gateway owns an in-process gRPC server, a loopback client connection, and
// the generated HTTP gateway.
type Gateway struct {
	Handler http.Handler

	server         *grpc.Server
	conn           *grpc.ClientConn
	listener       net.Listener
	extraListeners []net.Listener
}

// Option customizes the gateway's gRPC listener.
type Option func(*options)

type options struct {
	grpcListener       net.Listener
	grpcExtraListeners []net.Listener
	vmController       VMController
	interfaceCounters  InterfaceCounterSource
	aclRuntime         ACLRuntime
	diskProvisioner    DiskProvisioner
	diskMutex          *sync.Mutex
	vmMutex            *sync.Mutex
	interfaceRuntime   InterfaceRuntime
	diagnostics        DiagnosticsRuntime
	networkRuntime     NetworkRuntime
	networkWriter      NetworkWriter
	logger             zerolog.Logger
	vppInterfaces      VPPInterfaceSource
	hostWriter         HostWriter
	hostTapWriter      HostTapWriter
	vnfRuntime         VNFRuntime
	vnfStatusProvider  VNFStatusProvider
	vnfWriter          VNFWriter
}

// WithGRPCListener exposes the generated gRPC server on listener instead of an
// in-process bufconn listener. The HTTP gateway connects to the same listener.
func WithGRPCListener(listener net.Listener) Option {
	return func(opts *options) {
		opts.grpcListener = listener
	}
}

// WithExtraGRPCListener additionally serves the same gRPC services on another
// listener, for example a TLS listener that requires client certificates. The
// HTTP gateway continues to dial the primary listener (or the in-process
// bufconn listener when no primary listener is configured).
func WithExtraGRPCListener(listener net.Listener) Option {
	return func(opts *options) {
		if listener != nil {
			opts.grpcExtraListeners = append(opts.grpcExtraListeners, listener)
		}
	}
}

// WithVMController enables VM lifecycle and metrics RPCs.
func WithVMController(controller VMController) Option {
	return func(opts *options) {
		opts.vmController = controller
	}
}

// WithInterfaceCounters enables VPP interface counters in VM metrics.
func WithInterfaceCounters(source InterfaceCounterSource) Option {
	return func(opts *options) {
		opts.interfaceCounters = source
	}
}

// WithACLRuntime enables ACL writes and VPP ACL reconciliation.
func WithACLRuntime(runtime ACLRuntime) Option {
	return func(opts *options) {
		opts.aclRuntime = runtime
	}
}

// WithDiskProvisioner enables disk creation and deletion.
func WithDiskProvisioner(provisioner DiskProvisioner) Option {
	return func(opts *options) {
		opts.diskProvisioner = provisioner
	}
}

// WithDiskMutex shares the host resource lock with the legacy API.
func WithDiskMutex(mutex *sync.Mutex) Option {
	return func(opts *options) {
		opts.diskMutex = mutex
	}
}

// WithVMMutex shares the VM lifecycle lock with the legacy API.
func WithVMMutex(mutex *sync.Mutex) Option {
	return func(opts *options) {
		opts.vmMutex = mutex
	}
}

// WithInterfaceRuntime enables VPP vhost-user interface creation and removal.
func WithInterfaceRuntime(runtime InterfaceRuntime) Option {
	return func(opts *options) {
		opts.interfaceRuntime = runtime
	}
}

// WithDiagnosticsRuntime enables troubleshoot and capture RPCs.
func WithDiagnosticsRuntime(runtime DiagnosticsRuntime) Option {
	return func(opts *options) {
		opts.diagnostics = runtime
	}
}

// WithNetworkRuntime enables network creation and deletion.
func WithNetworkRuntime(runtime NetworkRuntime) Option {
	return func(opts *options) {
		opts.networkRuntime = runtime
	}
}

// WithNetworkWriter enables full network lifecycle deletion through internal/api.
func WithNetworkWriter(writer NetworkWriter) Option {
	return func(opts *options) {
		opts.networkWriter = writer
	}
}

// WithVNFRuntime enables DHCP lease and running-state views.
func WithVNFRuntime(runtime VNFRuntime) Option {
	return func(opts *options) {
		opts.vnfRuntime = runtime
	}
}

// WithVNFStatusProvider shares the legacy API's VNF status/NAT views.
func WithVNFStatusProvider(provider VNFStatusProvider) Option {
	return func(opts *options) {
		opts.vnfStatusProvider = provider
	}
}

// WithVNFWriter enables native VNF create/update/delete/restart operations.
func WithVNFWriter(writer VNFWriter) Option {
	return func(opts *options) {
		opts.vnfWriter = writer
	}
}

// WithLogger sets the gateway logger used by disk preparation scripts.
func WithLogger(logger zerolog.Logger) Option {
	return func(opts *options) {
		opts.logger = logger
	}
}

// WithVPPInterfaceSource lets the host-interface view correlate Linux NICs
// with their VPP host-interface counterparts.
func WithVPPInterfaceSource(source VPPInterfaceSource) Option {
	return func(opts *options) {
		opts.vppInterfaces = source
	}
}

// WithHostWriter enables attaching and detaching host interfaces in VPP.
func WithHostWriter(writer HostWriter) Option {
	return func(opts *options) {
		opts.hostWriter = writer
	}
}

// WithHostTapWriter enables persistent network-to-host taps.
func WithHostTapWriter(writer HostTapWriter) Option {
	return func(opts *options) {
		opts.hostTapWriter = writer
	}
}

// New builds the generated read-only gateway. By default it uses an in-process
// bufconn listener for tests and embedding. Use WithGRPCListener to expose gRPC
// on a real Unix or TCP socket.
func New(st *store.Store, ready Readiness, opts ...Option) (*Gateway, error) {
	options := &options{logger: zerolog.Nop()}
	for _, option := range opts {
		option(options)
	}
	if options.diskMutex == nil {
		options.diskMutex = &sync.Mutex{}
	}
	if options.vmMutex == nil {
		options.vmMutex = &sync.Mutex{}
	}
	listener := options.grpcListener
	useBufconn := listener == nil
	if useBufconn {
		listener = bufconn.Listen(1 << 20)
	}
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(errorLogInterceptor(options.logger), validationInterceptor))
	system := &systemService{store: st, readiness: ready}
	vms := &vmService{store: st, runtime: options.vmController, counters: options.interfaceCounters, readiness: ready, resourceMu: options.diskMutex, mu: options.vmMutex}
	disks := &diskService{store: st, runtime: options.diskProvisioner, readiness: ready, mu: options.diskMutex, logger: options.logger}
	media := &mediaService{store: st}
	networks := &networkService{store: st, runtime: options.networkRuntime, readiness: ready, writer: options.networkWriter, mu: options.diskMutex}
	interfaces := &interfaceService{store: st, runtime: options.aclRuntime, attachments: options.interfaceRuntime, vms: options.vmController, counters: options.interfaceCounters, readiness: ready, resourceMu: options.diskMutex, runMu: options.vmMutex}
	acls := &aclService{store: st, runtime: options.aclRuntime, readiness: ready}
	routeTables := &routeTableService{store: st, runtime: options.aclRuntime, readiness: ready}
	vnfs := &vnfService{store: st, runtime: options.vnfRuntime, statusProvider: options.vnfStatusProvider, writer: options.vnfWriter}
	backends := &storageBackendService{store: st, readiness: ready, disks: options.diskProvisioner, mu: options.diskMutex}
	hostInterfaces := &hostService{source: options.vppInterfaces, store: st, writer: options.hostWriter, taps: options.hostTapWriter}
	troubleshoot := &troubleshootService{runtime: options.diagnostics, readiness: ready}
	captures := &captureService{runtime: options.diagnostics, readiness: ready}
	rookeryv1.RegisterSystemServiceServer(server, system)
	rookeryv1.RegisterVMServiceServer(server, vms)
	rookeryv1.RegisterDiskServiceServer(server, disks)
	rookeryv1.RegisterMediaServiceServer(server, media)
	rookeryv1.RegisterNetworkServiceServer(server, networks)
	rookeryv1.RegisterInterfaceServiceServer(server, interfaces)
	rookeryv1.RegisterACLServiceServer(server, acls)
	rookeryv1.RegisterRouteTableServiceServer(server, routeTables)
	rookeryv1.RegisterVNFServiceServer(server, vnfs)
	rookeryv1.RegisterStorageBackendServiceServer(server, backends)
	rookeryv1.RegisterHostServiceServer(server, hostInterfaces)
	rookeryv1.RegisterTroubleshootServiceServer(server, troubleshoot)
	rookeryv1.RegisterCaptureServiceServer(server, captures)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	reflection.Register(server)

	go func() {
		_ = server.Serve(listener)
	}()
	for _, extra := range options.grpcExtraListeners {
		go func(listener net.Listener) {
			_ = server.Serve(listener)
		}(extra)
	}

	dialTarget := listener.Addr().String()
	dialOptions := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if useBufconn {
		dialTarget = "passthrough:///rookery-gateway"
		dialOptions = append(dialOptions, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.(*bufconn.Listener).DialContext(ctx)
		}))
	}
	conn, err := grpc.NewClient(dialTarget, dialOptions...)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		for _, extra := range options.grpcExtraListeners {
			_ = extra.Close()
		}
		return nil, err
	}

	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, newNumericJSONMarshaler()),
		runtime.WithErrorHandler(errorHandler),
		runtime.WithForwardResponseOption(httpResponseStatus),
		runtime.WithForwardResponseRewriter(rewriteNativeResponses),
	)
	ctx := context.Background()
	registrations := []func(context.Context, *runtime.ServeMux, *grpc.ClientConn) error{
		rookeryv1.RegisterSystemServiceHandler,
		rookeryv1.RegisterVMServiceHandler,
		rookeryv1.RegisterDiskServiceHandler,
		rookeryv1.RegisterMediaServiceHandler,
		rookeryv1.RegisterNetworkServiceHandler,
		rookeryv1.RegisterInterfaceServiceHandler,
		rookeryv1.RegisterACLServiceHandler,
		rookeryv1.RegisterRouteTableServiceHandler,
		rookeryv1.RegisterVNFServiceHandler,
		rookeryv1.RegisterStorageBackendServiceHandler,
		rookeryv1.RegisterHostServiceHandler,
		rookeryv1.RegisterTroubleshootServiceHandler,
		rookeryv1.RegisterCaptureServiceHandler,
	}
	for _, register := range registrations {
		if err := register(ctx, mux, conn); err != nil {
			_ = conn.Close()
			server.Stop()
			_ = listener.Close()
			for _, extra := range options.grpcExtraListeners {
				_ = extra.Close()
			}
			return nil, err
		}
	}

	return &Gateway{
		Handler: mux, server: server, conn: conn,
		listener: listener, extraListeners: options.grpcExtraListeners,
	}, nil
}

// GRPCAddress returns the address of the exposed gRPC listener.
func (g *Gateway) GRPCAddress() string {
	if g == nil || g.listener == nil {
		return ""
	}
	return g.listener.Addr().String()
}

// Close stops the loopback gRPC server and client.
func (g *Gateway) Close() {
	if g == nil {
		return
	}
	if g.conn != nil {
		_ = g.conn.Close()
	}
	if g.server != nil {
		g.server.Stop()
	}
	if g.listener != nil {
		_ = g.listener.Close()
	}
	for _, listener := range g.extraListeners {
		_ = listener.Close()
	}
}

// errorLogInterceptor logs every failed RPC with its gRPC method, status code,
// and underlying error. The request logger only records the HTTP status, so
// without this the cause of a 500 (for example a failed QEMU start) is lost.
func errorLogInterceptor(logger zerolog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		response, err := handler(ctx, req)
		if err != nil {
			logger.Error().
				Str("method", info.FullMethod).
				Str("code", status.Code(err).String()).
				Err(err).
				Msg("API request failed")
		}
		return response, err
	}
}

func validationInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if validator, ok := req.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	return handler(ctx, req)
}

func rewriteNativeResponses(ctx context.Context, response proto.Message) (interface{}, error) {
	if method, ok := runtime.RPCMethod(ctx); ok {
		switch method {
		case "/rookery.v1.VMService/ResetVM", "/rookery.v1.VMService/CtrlAltDel":
			return &emptypb.Empty{}, nil
		}
	}
	return response, nil
}

func errorHandler(_ context.Context, _ *runtime.ServeMux, _ runtime.Marshaler, w http.ResponseWriter, _ *http.Request, err error) {
	st := status.Convert(err)
	payload, marshalErr := json.Marshal(map[string]string{"error": st.Message()})
	if marshalErr != nil {
		payload = []byte(`{"error":"request failed"}`)
	}
	httpStatus := runtime.HTTPStatusFromCode(st.Code())
	if st.Code() == codes.Unimplemented {
		httpStatus = http.StatusMethodNotAllowed
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(payload)
}

type systemService struct {
	rookeryv1.UnimplementedSystemServiceServer
	store     *store.Store
	readiness Readiness
}

func (s *systemService) GetHealth(ctx context.Context, _ *rookeryv1.GetHealthRequest) (*rookeryv1.HealthResponse, error) {
	if err := s.store.Ping(ctx); err != nil {
		return nil, status.Error(codes.Unavailable, "database unavailable")
	}
	return &rookeryv1.HealthResponse{Status: "ok"}, nil
}

func (s *systemService) GetStatus(_ context.Context, _ *rookeryv1.GetStatusRequest) (*rookeryv1.StatusResponse, error) {
	current := s.readiness.Status()
	response := &rookeryv1.StatusResponse{Ready: current.Ready}
	if current.CheckedAt != nil {
		response.CheckedAt = timestamppb.New(*current.CheckedAt)
	}
	response.ThinPool = thinPoolStatus(current.ThinPool)
	response.Vpp = vppStatus(current.VPP)
	return response, nil
}

func thinPoolStatus(value readiness.ThinPoolStatus) *rookeryv1.ThinPoolStatus {
	result := &rookeryv1.ThinPoolStatus{
		Ready: value.Ready, State: value.State, Detail: value.Detail,
		Vg: value.VG, Name: value.Name,
	}
	if value.CheckedAt != nil {
		result.CheckedAt = timestamppb.New(*value.CheckedAt)
	}
	return result
}

func vppStatus(value readiness.VPPStatus) *rookeryv1.VPPStatus {
	result := &rookeryv1.VPPStatus{
		Ready: value.Ready, State: value.State, Detail: value.Detail, Socket: value.Socket,
	}
	if value.CheckedAt != nil {
		result.CheckedAt = timestamppb.New(*value.CheckedAt)
	}
	return result
}

type vmService struct {
	rookeryv1.UnimplementedVMServiceServer
	store      *store.Store
	runtime    VMController
	counters   InterfaceCounterSource
	readiness  Readiness
	resourceMu *sync.Mutex
	mu         *sync.Mutex
}

func (s *vmService) ListVMs(ctx context.Context, _ *rookeryv1.ListVMsRequest) (*rookeryv1.ListVMsResponse, error) {
	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list VMs")
	}
	response := &rookeryv1.ListVMsResponse{Vms: make([]*rookeryv1.VM, 0, len(vms))}
	for _, vm := range vms {
		response.Vms = append(response.Vms, vmToProto(vm))
	}
	return response, nil
}

func (s *vmService) GetVM(ctx context.Context, request *rookeryv1.GetVMRequest) (*rookeryv1.VM, error) {
	vm, err := s.store.GetVM(ctx, request.GetId())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "vm not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not read VM")
	default:
		return vmToProto(vm), nil
	}
}

func vmToProto(vm store.VM) *rookeryv1.VM {
	result := &rookeryv1.VM{
		Id:             vm.ID,
		Name:           vm.Name,
		Description:    vm.Description,
		Status:         vm.Status,
		Vcpus:          int32(vm.VCPUs),
		MemoryMb:       int32(vm.MemoryMB),
		DiskId:         vm.DiskID,
		BootMediaId:    vm.BootMediaID,
		ConsoleEnabled: vm.ConsoleEnabled,
	}
	if !vm.CreatedAt.IsZero() {
		result.CreatedAt = timestamppb.New(vm.CreatedAt)
	}
	if !vm.UpdatedAt.IsZero() {
		result.UpdatedAt = timestamppb.New(vm.UpdatedAt)
	}
	if vm.Disk != nil {
		result.Disk = vmDiskToProto(*vm.Disk)
	}
	for _, disk := range vm.Disks {
		result.Disks = append(result.Disks, vmDiskToProto(disk))
		result.DiskIds = append(result.DiskIds, disk.ID)
	}
	if vm.BootMedia != nil {
		result.BootMedia = &rookeryv1.VMMedia{
			Id: vm.BootMedia.ID, Name: vm.BootMedia.Name, Path: vm.BootMedia.Path, SizeBytes: vm.BootMedia.SizeBytes,
		}
	}
	for _, iface := range vm.Interfaces {
		result.Interfaces = append(result.Interfaces, &rookeryv1.VMInterface{
			Id: iface.ID, Name: iface.Name, NetworkId: iface.NetworkID, NetworkName: iface.NetworkName,
			SocketPath: iface.SocketPath, Mac: iface.MAC, SwIfIndex: iface.SwIfIndex,
			IngressAclName: iface.IngressACLName, EgressAclName: iface.EgressACLName,
		})
	}
	return result
}

func vmDiskToProto(disk store.VMDisk) *rookeryv1.VMDisk {
	return &rookeryv1.VMDisk{
		Id: disk.ID, Name: disk.Name, SizeGb: int32(disk.SizeGB),
		StorageBackendId: disk.StorageBackendID, StorageBackendName: disk.StorageBackendName,
		Vg: disk.VG, ThinPool: disk.ThinPool, Device: disk.Device,
	}
}
