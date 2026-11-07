package storage

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/store"
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
	if f.getVolume == nil {
		return nil, nil
	}
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

func storageKoujaBackend(t *testing.T) store.StorageBackend {
	t.Helper()
	certFile, keyFile, caFile := storageTestCert(t)
	return store.StorageBackend{
		ID: 1, Name: "kouja", Driver: store.DiskSourceKouja,
		Config: store.StorageBackendConfig{Kouja: &store.KoujaConfig{
			Endpoint: "10.0.0.1:9443", Protocol: "iscsi", StorageClass: "nvme",
			Initiator: "iqn.2026-11.io.example:rookery",
			CAFile:    caFile, ClientCertFile: certFile, ClientKeyFile: keyFile,
		}},
	}
}

func storageKoujaDriver(api *fakeAPI) *KoujaDriver {
	return &KoujaDriver{newClient: func(kouja.Config) (*kouja.Client, error) {
		return kouja.NewClientFromService(api), nil
	}}
}

func storageTestCert(t *testing.T) (string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := url.Parse("spiffe://kouja/caller/rookery")
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "rookery"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, IsCA: true,
		URIs: []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile, caFile := filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key"), filepath.Join(dir, "ca.crt")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, caFile
}

func storageReference() blockdev.Reference {
	return blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
		Portal: "10.0.0.1:3260", ExportPortal: "10.0.0.1:3260",
		Target: "iqn.2026-11.io.example:disk", LUN: 0, Initiator: "iqn.2026-11.io.example:rookery",
	}}
}

func TestKoujaDriverCreate(t *testing.T) {
	api := &fakeAPI{create: func(_ context.Context, request *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
		if request.GetStorageClass() != "nvme" || request.GetProtocol() != koujav1.Protocol_PROTOCOL_ISCSI {
			t.Fatalf("request = %+v", request)
		}
		return &koujav1.CreateVolumeResponse{
			Volume: &koujav1.Volume{Owner: "spiffe://kouja/caller/rookery", VolumeId: "volume-1",
				ResourceUuid: "resource-1", ActualSizeBytes: 10 << 30, State: koujav1.VolumeState_VOLUME_STATE_EXPORTED},
			Export: &koujav1.Export{Protocol: koujav1.Protocol_PROTOCOL_ISCSI,
				TargetName: "iqn.2026-11.io.example:disk", Portal: "10.0.0.1:3260", Lun: 0,
				State: koujav1.ExportState_EXPORT_STATE_EXPORTED},
		}, nil
	}}
	driver := storageKoujaDriver(api)
	created, err := driver.CreateDisk(context.Background(), storageKoujaBackend(t), CreateDiskRequest{
		Disk: store.Disk{SizeGB: 10, BackendMetadata: store.DiskBackendMetadata{
			Volume: store.VolumeMetadata{VolumeID: "volume-1"},
		}},
		RequestID: "request-1", SizeBytes: 10 << 30,
	})
	if err != nil {
		t.Fatalf("CreateDisk() = %v", err)
	}
	if created.Metadata.Volume.ResourceID != "resource-1" || created.Metadata.Attachment.ISCSI == nil ||
		created.SizeBytes != 10<<30 {
		t.Fatalf("created = %+v", created)
	}
}

func TestKoujaDriverDeleteAndOrphan(t *testing.T) {
	unexported, deleted := false, false
	api := &fakeAPI{
		unexport: func(context.Context, *koujav1.UnexportVolumeRequest) (*koujav1.UnexportVolumeResponse, error) {
			unexported = true
			return &koujav1.UnexportVolumeResponse{}, nil
		},
		delete: func(context.Context, *koujav1.DeleteVolumeRequest) (*koujav1.DeleteVolumeResponse, error) {
			deleted = true
			return &koujav1.DeleteVolumeResponse{}, nil
		},
	}
	driver := storageKoujaDriver(api)
	disk := store.Disk{BackendMetadata: store.DiskBackendMetadata{Volume: store.VolumeMetadata{VolumeID: "volume-1"}}}
	if err := driver.DeleteDisk(context.Background(), storageKoujaBackend(t), disk); err != nil {
		t.Fatal(err)
	}
	if !unexported || !deleted {
		t.Fatalf("unexported=%t deleted=%t", unexported, deleted)
	}
	unexported, deleted = false, false
	if err := driver.DeleteOrphanVolume(context.Background(), storageKoujaBackend(t), disk); err != nil {
		t.Fatal(err)
	}
	if unexported || !deleted {
		t.Fatalf("orphan unexported=%t deleted=%t", unexported, deleted)
	}
}

func TestKoujaDriverPrepareAndHealth(t *testing.T) {
	api := &fakeAPI{
		getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			return &koujav1.GetVolumeResponse{
				Desired: &koujav1.Volume{Owner: "spiffe://kouja/caller/rookery", State: koujav1.VolumeState_VOLUME_STATE_EXPORTED},
				Export: &koujav1.Export{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, TargetName: "iqn.2026-11.io.example:disk",
					Portal: "10.0.0.1:3260", Lun: 0, State: koujav1.ExportState_EXPORT_STATE_EXPORTED},
			}, nil
		},
		getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
			return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, Credential: "secret"}, nil
		},
		getInfo: func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error) {
			return &koujav1.GetInfoResponse{Ready: true, Protocols: []koujav1.Protocol{koujav1.Protocol_PROTOCOL_ISCSI}}, nil
		},
		listClasses: func(context.Context, *koujav1.ListStorageClassesRequest) (*koujav1.ListStorageClassesResponse, error) {
			return &koujav1.ListStorageClassesResponse{Classes: []*koujav1.StorageClass{{Name: "nvme"}}}, nil
		},
	}
	driver := storageKoujaDriver(api)
	disk := store.Disk{Protocol: string(blockdev.ProtocolISCSI), BackendMetadata: store.DiskBackendMetadata{
		Volume:     store.VolumeMetadata{VolumeID: "volume-1", ResourceID: "resource-1", Owner: "spiffe://kouja/caller/rookery"},
		Attachment: storageReference(),
	}}
	attachment, err := driver.PrepareStart(context.Background(), storageKoujaBackend(t), disk)
	if err != nil || attachment.ISCSI == nil || attachment.ISCSI.Credential != "secret" {
		t.Fatalf("PrepareStart = %+v, %v", attachment, err)
	}
	health, err := driver.Health(context.Background(), storageKoujaBackend(t), nil)
	if err != nil || !health.Ready || len(health.Classes) != 1 {
		t.Fatalf("Health = %+v, %v", health, err)
	}
}

func TestKoujaDriverErrorBranches(t *testing.T) {
	ctx := context.Background()
	badBackend := store.StorageBackend{Config: store.StorageBackendConfig{}}
	valid := storageKoujaBackend(t)
	if NewKoujaDriver().Kind() != store.DiskSourceKouja {
		t.Fatal("NewKoujaDriver kind mismatch")
	}
	driver := storageKoujaDriver(&fakeAPI{})
	disk := store.Disk{SizeGB: 5, BackendMetadata: store.DiskBackendMetadata{Volume: store.VolumeMetadata{VolumeID: "volume-1"}}}
	if _, err := driver.CreateDisk(ctx, badBackend, CreateDiskRequest{Disk: disk}); err == nil {
		t.Fatal("CreateDisk accepted bad config")
	}
	badClient := &KoujaDriver{newClient: func(kouja.Config) (*kouja.Client, error) { return nil, errors.New("boom") }}
	if _, err := badClient.CreateDisk(ctx, valid, CreateDiskRequest{Disk: disk}); err == nil {
		t.Fatal("CreateDisk accepted client error")
	}
	missing := store.Disk{}
	if _, err := driver.CreateDisk(ctx, valid, CreateDiskRequest{Disk: missing}); err == nil {
		t.Fatal("CreateDisk accepted missing volume ID")
	}
	// Size and request-ID defaults.
	api := &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
		return &koujav1.CreateVolumeResponse{
			Volume: &koujav1.Volume{Owner: "spiffe://kouja/caller/rookery", VolumeId: "volume-1",
				ResourceUuid: "resource-1", ActualSizeBytes: 0, State: koujav1.VolumeState_VOLUME_STATE_EXPORTED},
			Export: &koujav1.Export{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, TargetName: "iqn.x",
				Portal: "10.0.0.1:3260", Lun: 0, State: koujav1.ExportState_EXPORT_STATE_EXPORTED},
		}, nil
	}}
	created, err := storageKoujaDriver(api).CreateDisk(ctx, valid, CreateDiskRequest{Disk: disk})
	if err != nil || created.SizeBytes != 5<<30 || created.Metadata.Volume.RequestID != "create-volume-1" {
		t.Fatalf("created = %+v, %v", created, err)
	}

	if err := driver.DeleteDisk(ctx, badBackend, disk); err == nil {
		t.Fatal("DeleteDisk accepted bad config")
	}
	if err := badClient.DeleteDisk(ctx, valid, disk); err == nil {
		t.Fatal("DeleteDisk accepted client error")
	}
	if err := driver.DeleteDisk(ctx, valid, store.Disk{}); err == nil {
		t.Fatal("DeleteDisk accepted missing volume ID")
	}
	if err := driver.DeleteDisk(ctx, valid, disk); err != nil {
		t.Fatalf("DeleteDisk() = %v", err)
	}
	unexportErr := &fakeAPI{unexport: func(context.Context, *koujav1.UnexportVolumeRequest) (*koujav1.UnexportVolumeResponse, error) {
		return nil, errors.New("boom")
	}}
	if err := storageKoujaDriver(unexportErr).DeleteDisk(ctx, valid, disk); err == nil {
		t.Fatal("DeleteDisk accepted unexport error")
	}
	deleteErr := &fakeAPI{delete: func(context.Context, *koujav1.DeleteVolumeRequest) (*koujav1.DeleteVolumeResponse, error) {
		return nil, errors.New("boom")
	}}
	if err := storageKoujaDriver(deleteErr).DeleteDisk(ctx, valid, disk); err == nil {
		t.Fatal("DeleteDisk accepted delete error")
	}

	if err := driver.DeleteOrphanVolume(ctx, badBackend, disk); err == nil {
		t.Fatal("DeleteOrphanVolume accepted bad config")
	}
	if err := badClient.DeleteOrphanVolume(ctx, valid, disk); err == nil {
		t.Fatal("DeleteOrphanVolume accepted client error")
	}
	if err := driver.DeleteOrphanVolume(ctx, valid, store.Disk{}); err != nil {
		t.Fatalf("DeleteOrphanVolume(empty) = %v", err)
	}
	if err := storageKoujaDriver(deleteErr).DeleteOrphanVolume(ctx, valid, disk); err == nil {
		t.Fatal("DeleteOrphanVolume accepted delete error")
	}

	if _, err := driver.PrepareStart(ctx, badBackend, disk); err == nil {
		t.Fatal("PrepareStart accepted bad config")
	}
	if _, err := badClient.PrepareStart(ctx, valid, disk); err == nil {
		t.Fatal("PrepareStart accepted client error")
	}
	if _, err := driver.Health(ctx, badBackend, nil); err == nil {
		t.Fatal("Health accepted bad config")
	}
	if _, err := badClient.Health(ctx, valid, nil); err == nil {
		t.Fatal("Health accepted client error")
	}
}

func TestKoujaDriverWaitsForSession(t *testing.T) {
	ctx := context.Background()
	calls := 0
	api := &fakeAPI{
		getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			calls++
			state := koujav1.SessionState_SESSION_STATE_ACTIVE
			if calls > 1 {
				state = koujav1.SessionState_SESSION_STATE_NONE
			}
			active := int32(0)
			if state == koujav1.SessionState_SESSION_STATE_ACTIVE {
				active = 1
			}
			return &koujav1.GetVolumeResponse{
				Desired:  &koujav1.Volume{Owner: "spiffe://kouja/caller/rookery", State: koujav1.VolumeState_VOLUME_STATE_EXPORTED},
				Observed: &koujav1.Observation{TargetPresent: true, AclPresent: true, ActiveSessions: active, ObservedAt: timestamppb.Now()},
				Export: &koujav1.Export{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, TargetName: "iqn.2026-11.io.example:disk",
					Portal: "10.0.0.1:3260", Lun: 0, State: koujav1.ExportState_EXPORT_STATE_EXPORTED, SessionState: state},
			}, nil
		},
		getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
			return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, Credential: "secret"}, nil
		},
	}
	driver := storageKoujaDriver(api)
	driver.sessionWait = time.Second
	driver.sessionPoll = time.Millisecond
	disk := store.Disk{Protocol: string(blockdev.ProtocolISCSI), BackendMetadata: store.DiskBackendMetadata{
		Volume:     store.VolumeMetadata{VolumeID: "volume-1", ResourceID: "resource-1", Owner: "spiffe://kouja/caller/rookery"},
		Attachment: storageReference(),
	}}
	attachment, err := driver.PrepareStart(ctx, storageKoujaBackend(t), disk)
	if err != nil || attachment.ISCSI == nil || calls < 2 {
		t.Fatalf("PrepareStart() = %+v, %v, calls=%d", attachment, err, calls)
	}

	alwaysActive := &fakeAPI{
		getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			return &koujav1.GetVolumeResponse{
				Desired:  &koujav1.Volume{Owner: "spiffe://kouja/caller/rookery", State: koujav1.VolumeState_VOLUME_STATE_EXPORTED},
				Observed: &koujav1.Observation{TargetPresent: true, AclPresent: true, ActiveSessions: 1, ObservedAt: timestamppb.Now()},
				Export: &koujav1.Export{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, TargetName: "iqn.2026-11.io.example:disk",
					Portal: "10.0.0.1:3260", Lun: 0, State: koujav1.ExportState_EXPORT_STATE_EXPORTED,
					SessionState: koujav1.SessionState_SESSION_STATE_ACTIVE},
			}, nil
		},
	}
	driver = storageKoujaDriver(alwaysActive)
	driver.sessionWait = 5 * time.Millisecond
	driver.sessionPoll = time.Millisecond
	if _, err := driver.PrepareStart(ctx, storageKoujaBackend(t), disk); err == nil || !strings.Contains(err.Error(), "after waiting") {
		t.Fatalf("PrepareStart(active) = %v", err)
	}
}
