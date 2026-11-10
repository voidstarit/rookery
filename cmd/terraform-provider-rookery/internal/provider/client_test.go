package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func testCertificatePEM(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "rookery-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func TestClientUploadTimeoutAndValidation(t *testing.T) {
	certPEM, keyPEM := testCertificatePEM(t)
	var nilClient *Client
	if nilClient.UploadTimeout() != 30*time.Minute {
		t.Fatalf("nil upload timeout = %v", nilClient.UploadTimeout())
	}
	client, err := NewClient(ClientConfig{
		Endpoint: "127.0.0.1:1", CAPEM: certPEM, CertPEM: certPEM, KeyPEM: keyPEM,
		UploadTimeoutRaw: "45m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.UploadTimeout() != 45*time.Minute || client.LightVMKernels == nil || client.LightVMArtifacts == nil || client.LightVMs == nil {
		t.Fatalf("client = %+v", client)
	}
	if _, err := NewClient(ClientConfig{
		Endpoint: "127.0.0.1:1", CAPEM: certPEM, CertPEM: certPEM, KeyPEM: keyPEM,
		UploadTimeoutRaw: "not-a-duration",
	}); err == nil {
		t.Fatal("invalid upload timeout was accepted")
	}
}

func TestProviderSchemaAndConfigure(t *testing.T) {
	ctx := context.Background()
	p := &RookeryProvider{}
	var schemaResponse provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &schemaResponse)
	if _, ok := schemaResponse.Schema.Attributes["upload_timeout"]; !ok {
		t.Fatal("upload_timeout schema attribute is missing")
	}
	certPEM, keyPEM := testCertificatePEM(t)
	model := providerModel{
		Endpoint: types.StringValue("127.0.0.1:1"),
		CAPEM:    types.StringValue(certPEM), CertPEM: types.StringValue(certPEM), KeyPEM: types.StringValue(keyPEM),
		UploadTimeout: types.StringValue("45m"),
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set provider config: %v", diags)
	}
	config := tfsdk.Config{Schema: schemaResponse.Schema, Raw: plan.Raw}
	response := provider.ConfigureResponse{}
	p.Configure(ctx, provider.ConfigureRequest{Config: config}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("configure: %v", response.Diagnostics)
	}
	client, ok := response.ResourceData.(*Client)
	if !ok || client.UploadTimeout() != 45*time.Minute {
		t.Fatalf("configured client = %+v", response.ResourceData)
	}
}
