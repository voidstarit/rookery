package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

// errVNFBoom is the sentinel API failure injected by the error-path fakes.
var errVNFBoom = errors.New("injected VNF API failure")

// failingDHCPVNFClient returns err from every DHCP service call the provider
// resources and data sources make.
type failingDHCPVNFClient struct {
	rookeryv1.DHCPVNFServiceClient
	err error
}

func (f *failingDHCPVNFClient) ListDHCPVNFs(context.Context, *rookeryv1.ListDHCPVNFsRequest, ...grpc.CallOption) (*rookeryv1.ListDHCPVNFsResponse, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) GetDHCPVNF(context.Context, *rookeryv1.GetDHCPVNFRequest, ...grpc.CallOption) (*rookeryv1.DHCPVNF, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) CreateDHCPVNF(context.Context, *rookeryv1.CreateDHCPVNFRequest, ...grpc.CallOption) (*rookeryv1.DHCPVNF, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) UpdateDHCPVNF(context.Context, *rookeryv1.UpdateDHCPVNFRequest, ...grpc.CallOption) (*rookeryv1.DHCPVNF, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) DeleteDHCPVNF(context.Context, *rookeryv1.DeleteDHCPVNFRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) GetDHCPStaticAssignment(context.Context, *rookeryv1.GetDHCPStaticAssignmentRequest, ...grpc.CallOption) (*rookeryv1.DHCPStaticAssignment, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) CreateDHCPStaticAssignment(context.Context, *rookeryv1.CreateDHCPStaticAssignmentRequest, ...grpc.CallOption) (*rookeryv1.DHCPStaticAssignment, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) UpdateDHCPStaticAssignment(context.Context, *rookeryv1.UpdateDHCPStaticAssignmentRequest, ...grpc.CallOption) (*rookeryv1.DHCPStaticAssignment, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) DeleteDHCPStaticAssignment(context.Context, *rookeryv1.DeleteDHCPStaticAssignmentRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) GetDHCPOption(context.Context, *rookeryv1.GetDHCPOptionRequest, ...grpc.CallOption) (*rookeryv1.DHCPOption, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) CreateDHCPOption(context.Context, *rookeryv1.CreateDHCPOptionRequest, ...grpc.CallOption) (*rookeryv1.DHCPOption, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) UpdateDHCPOption(context.Context, *rookeryv1.UpdateDHCPOptionRequest, ...grpc.CallOption) (*rookeryv1.DHCPOption, error) {
	return nil, f.err
}

func (f *failingDHCPVNFClient) DeleteDHCPOption(context.Context, *rookeryv1.DeleteDHCPOptionRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

// failingDNSVNFClient returns err from every DNS service call the provider
// resources and data sources make.
type failingDNSVNFClient struct {
	rookeryv1.DNSVNFServiceClient
	err error
}

func (f *failingDNSVNFClient) ListDNSVNFs(context.Context, *rookeryv1.ListDNSVNFsRequest, ...grpc.CallOption) (*rookeryv1.ListDNSVNFsResponse, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) GetDNSVNF(context.Context, *rookeryv1.GetDNSVNFRequest, ...grpc.CallOption) (*rookeryv1.DNSVNF, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) CreateDNSVNF(context.Context, *rookeryv1.CreateDNSVNFRequest, ...grpc.CallOption) (*rookeryv1.DNSVNF, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) UpdateDNSVNF(context.Context, *rookeryv1.UpdateDNSVNFRequest, ...grpc.CallOption) (*rookeryv1.DNSVNF, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) DeleteDNSVNF(context.Context, *rookeryv1.DeleteDNSVNFRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) GetDNSVNFRecord(context.Context, *rookeryv1.GetDNSVNFRecordRequest, ...grpc.CallOption) (*rookeryv1.DNSRecord, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) CreateDNSVNFRecord(context.Context, *rookeryv1.CreateDNSVNFRecordRequest, ...grpc.CallOption) (*rookeryv1.DNSRecord, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) UpdateDNSVNFRecord(context.Context, *rookeryv1.UpdateDNSVNFRecordRequest, ...grpc.CallOption) (*rookeryv1.DNSRecord, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) DeleteDNSVNFRecord(context.Context, *rookeryv1.DeleteDNSVNFRecordRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) GetDNSVNFForwarder(context.Context, *rookeryv1.GetDNSVNFForwarderRequest, ...grpc.CallOption) (*rookeryv1.DNSForwarder, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) CreateDNSVNFForwarder(context.Context, *rookeryv1.CreateDNSVNFForwarderRequest, ...grpc.CallOption) (*rookeryv1.DNSForwarder, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) UpdateDNSVNFForwarder(context.Context, *rookeryv1.UpdateDNSVNFForwarderRequest, ...grpc.CallOption) (*rookeryv1.DNSForwarder, error) {
	return nil, f.err
}

func (f *failingDNSVNFClient) DeleteDNSVNFForwarder(context.Context, *rookeryv1.DeleteDNSVNFForwarderRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

// failingNAT44VNFClient returns err from every NAT44 service call the provider
// resources and data sources make.
type failingNAT44VNFClient struct {
	rookeryv1.NAT44VNFServiceClient
	err error
}

func (f *failingNAT44VNFClient) ListNAT44VNFs(context.Context, *rookeryv1.ListNAT44VNFsRequest, ...grpc.CallOption) (*rookeryv1.ListNAT44VNFsResponse, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) GetNAT44VNF(context.Context, *rookeryv1.GetNAT44VNFRequest, ...grpc.CallOption) (*rookeryv1.NAT44VNF, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) CreateNAT44VNF(context.Context, *rookeryv1.CreateNAT44VNFRequest, ...grpc.CallOption) (*rookeryv1.NAT44VNF, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) UpdateNAT44VNF(context.Context, *rookeryv1.UpdateNAT44VNFRequest, ...grpc.CallOption) (*rookeryv1.NAT44VNF, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) DeleteNAT44VNF(context.Context, *rookeryv1.DeleteNAT44VNFRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) GetNAT44DNATRule(context.Context, *rookeryv1.GetNAT44DNATRuleRequest, ...grpc.CallOption) (*rookeryv1.DNATRule, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) CreateNAT44DNATRule(context.Context, *rookeryv1.CreateNAT44DNATRuleRequest, ...grpc.CallOption) (*rookeryv1.DNATRule, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) UpdateNAT44DNATRule(context.Context, *rookeryv1.UpdateNAT44DNATRuleRequest, ...grpc.CallOption) (*rookeryv1.DNATRule, error) {
	return nil, f.err
}

func (f *failingNAT44VNFClient) DeleteNAT44DNATRule(context.Context, *rookeryv1.DeleteNAT44DNATRuleRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

// failingBGPVNFClient returns err from every BGP service call the provider
// resources and data sources make.
type failingBGPVNFClient struct {
	rookeryv1.BGPVNFServiceClient
	err error
}

func (f *failingBGPVNFClient) ListBGPVNFs(context.Context, *rookeryv1.ListBGPVNFsRequest, ...grpc.CallOption) (*rookeryv1.ListBGPVNFsResponse, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) GetBGPVNF(context.Context, *rookeryv1.GetBGPVNFRequest, ...grpc.CallOption) (*rookeryv1.BGPVNF, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) CreateBGPVNF(context.Context, *rookeryv1.CreateBGPVNFRequest, ...grpc.CallOption) (*rookeryv1.BGPVNF, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) UpdateBGPVNF(context.Context, *rookeryv1.UpdateBGPVNFRequest, ...grpc.CallOption) (*rookeryv1.BGPVNF, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) DeleteBGPVNF(context.Context, *rookeryv1.DeleteBGPVNFRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) GetBGPVNFAdvertisedPrefix(context.Context, *rookeryv1.GetBGPVNFAdvertisedPrefixRequest, ...grpc.CallOption) (*rookeryv1.BGPPrefix, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) CreateBGPVNFAdvertisedPrefix(context.Context, *rookeryv1.CreateBGPVNFAdvertisedPrefixRequest, ...grpc.CallOption) (*rookeryv1.BGPPrefix, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) UpdateBGPVNFAdvertisedPrefix(context.Context, *rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest, ...grpc.CallOption) (*rookeryv1.BGPPrefix, error) {
	return nil, f.err
}

func (f *failingBGPVNFClient) DeleteBGPVNFAdvertisedPrefix(context.Context, *rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, f.err
}

// failingLabelClient fails label writes and reads individually so a resource
// create, read, or update can reach the label branch.
type failingLabelClient struct {
	rookeryv1.LabelServiceClient
	setErr error
	getErr error
}

func (f *failingLabelClient) SetEntityLabels(context.Context, *rookeryv1.SetEntityLabelsRequest, ...grpc.CallOption) (*rookeryv1.EntityLabels, error) {
	return nil, f.setErr
}

func (f *failingLabelClient) GetEntityLabels(context.Context, *rookeryv1.GetEntityLabelsRequest, ...grpc.CallOption) (*rookeryv1.EntityLabels, error) {
	return nil, f.getErr
}

// failingVNFClient is a fully configured client whose four typed services all
// return an injected API error.
func failingVNFClient() *Client {
	client := newFakeVNFBackend().client()
	client.DHCPVNFs = &failingDHCPVNFClient{err: errVNFBoom}
	client.DNSVNFs = &failingDNSVNFClient{err: errVNFBoom}
	client.NAT44VNFs = &failingNAT44VNFClient{err: errVNFBoom}
	client.BGPVNFs = &failingBGPVNFClient{err: errVNFBoom}
	return client
}

// requireDiagError fails the test when diags does not report an error.
func requireDiagError(t *testing.T, what string, diags diag.Diagnostics) {
	t.Helper()
	if !diags.HasError() {
		t.Fatalf("%s: expected an error diagnostic", what)
	}
}

// vnfResourceErrorCase wires one resource into the shared error-path matrix.
type vnfResourceErrorCase struct {
	name     string
	newValue func(*Client) resource.Resource
	model    func() any
	parent   bool
}

func vnfResourceErrorCases() []vnfResourceErrorCase {
	return []vnfResourceErrorCase{
		{
			name:     "dhcp vnf",
			newValue: func(client *Client) resource.Resource { return &dhcpVNFResource{configuredResource{client: client}} },
			model:    func() any { return testDHCPVNFModel() },
			parent:   true,
		},
		{
			name: "dhcp static assignment",
			newValue: func(client *Client) resource.Resource {
				return &dhcpStaticAssignmentResource{configuredResource{client: client}}
			},
			model: func() any { return testDHCPStaticAssignmentModel() },
		},
		{
			name:     "dhcp option",
			newValue: func(client *Client) resource.Resource { return &dhcpOptionResource{configuredResource{client: client}} },
			model:    func() any { return testDHCPOptionModel() },
		},
		{
			name:     "dns vnf",
			newValue: func(client *Client) resource.Resource { return &dnsVNFResource{configuredResource{client: client}} },
			model:    func() any { return testDNSVNFModel() },
			parent:   true,
		},
		{
			name:     "dns record",
			newValue: func(client *Client) resource.Resource { return &dnsRecordResource{configuredResource{client: client}} },
			model:    func() any { return testDNSRecordModel() },
		},
		{
			name: "dns forwarder",
			newValue: func(client *Client) resource.Resource {
				return &dnsForwarderResource{configuredResource{client: client}}
			},
			model: func() any { return testDNSForwarderModel() },
		},
		{
			name:     "nat44 vnf",
			newValue: func(client *Client) resource.Resource { return &nat44VNFResource{configuredResource{client: client}} },
			model:    func() any { return testNAT44VNFModel() },
			parent:   true,
		},
		{
			name: "nat44 dnat rule",
			newValue: func(client *Client) resource.Resource {
				return &nat44DNATRuleResource{configuredResource{client: client}}
			},
			model: func() any { return testNAT44DNATRuleModel() },
		},
		{
			name:     "bgp vnf",
			newValue: func(client *Client) resource.Resource { return &bgpVNFResource{configuredResource{client: client}} },
			model:    func() any { return testBGPVNFModel() },
			parent:   true,
		},
		{
			name: "bgp advertised prefix",
			newValue: func(client *Client) resource.Resource {
				return &bgpAdvertisedPrefixResource{configuredResource{client: client}}
			},
			model: func() any { return testBGPAdvertisedPrefixModel() },
		},
	}
}

// TestVNFResourceErrorPaths drives every failure branch of the ten VNF
// resources: an undecodable plan or state, an unconfigured client, an API
// error, a label failure, an absent-resource delete, and an invalid import id.
func TestVNFResourceErrorPaths(t *testing.T) {
	ctx := context.Background()
	for _, test := range vnfResourceErrorCases() {
		t.Run(test.name, func(t *testing.T) {
			healthy := test.newValue(newFakeVNFBackend().client())
			schema := resourceSchema(t, healthy)
			baseModel := test.model()

			t.Run("create rejects an undecodable plan", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
				value.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schema}}, &createResponse)
				requireDiagError(t, "create with a null plan", createResponse.Diagnostics)
			})

			t.Run("create requires a configured client", func(t *testing.T) {
				value := test.newValue(nil)
				plan := vnfPlanFromModel(t, schema, baseModel)
				createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
				value.Create(ctx, resource.CreateRequest{Plan: plan, Config: configFromPlan(schema, plan)}, &createResponse)
				requireDiagError(t, "create without a client", createResponse.Diagnostics)
			})

			t.Run("create reports the API error", func(t *testing.T) {
				value := test.newValue(failingVNFClient())
				plan := vnfPlanFromModel(t, schema, baseModel)
				createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
				value.Create(ctx, resource.CreateRequest{Plan: plan, Config: configFromPlan(schema, plan)}, &createResponse)
				requireDiagError(t, "create with an API failure", createResponse.Diagnostics)
			})

			if test.parent {
				t.Run("create reports a label error", func(t *testing.T) {
					client := newFakeVNFBackend().client()
					client.Labels = &failingLabelClient{setErr: errVNFBoom}
					value := test.newValue(client)
					plan := vnfPlanFromModel(t, schema, baseModel)
					createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
					value.Create(ctx, resource.CreateRequest{Plan: plan, Config: configFromPlan(schema, plan)}, &createResponse)
					requireDiagError(t, "create with a label failure", createResponse.Diagnostics)
				})
			}

			t.Run("read rejects an undecodable state", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				readResponse := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
				value.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schema}}, &readResponse)
				requireDiagError(t, "read with a null state", readResponse.Diagnostics)
			})

			t.Run("read requires a configured client", func(t *testing.T) {
				value := test.newValue(nil)
				state := vnfStateFromModel(t, schema, baseModel)
				readResponse := resource.ReadResponse{State: state}
				value.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
				requireDiagError(t, "read without a client", readResponse.Diagnostics)
			})

			t.Run("read reports the API error", func(t *testing.T) {
				value := test.newValue(failingVNFClient())
				state := vnfStateFromModel(t, schema, baseModel)
				readResponse := resource.ReadResponse{State: state}
				value.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
				requireDiagError(t, "read with an API failure", readResponse.Diagnostics)
			})

			if test.parent {
				t.Run("read reports a label error", func(t *testing.T) {
					backend := newFakeVNFBackend()
					backend.seed()
					client := backend.client()
					client.Labels = &failingLabelClient{getErr: errVNFBoom}
					value := test.newValue(client)
					stateModel := withModelAttribute(t, baseModel, "id", types.Int64Value(7))
					state := vnfStateFromModel(t, schema, stateModel)
					readResponse := resource.ReadResponse{State: state}
					value.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
					requireDiagError(t, "read with a label failure", readResponse.Diagnostics)
				})
			}

			t.Run("update rejects an undecodable plan", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
				value.Update(ctx, resource.UpdateRequest{
					Plan:  tfsdk.Plan{Schema: schema},
					State: vnfStateFromModel(t, schema, baseModel),
				}, &updateResponse)
				requireDiagError(t, "update with a null plan", updateResponse.Diagnostics)
			})

			t.Run("update requires a configured client", func(t *testing.T) {
				value := test.newValue(nil)
				plan := vnfPlanFromModel(t, schema, baseModel)
				updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
				value.Update(ctx, resource.UpdateRequest{
					Plan:   plan,
					Config: configFromPlan(schema, plan),
					State:  vnfStateFromModel(t, schema, baseModel),
				}, &updateResponse)
				requireDiagError(t, "update without a client", updateResponse.Diagnostics)
			})

			t.Run("update reports the API error", func(t *testing.T) {
				value := test.newValue(failingVNFClient())
				plan := vnfPlanFromModel(t, schema, baseModel)
				updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
				value.Update(ctx, resource.UpdateRequest{
					Plan:   plan,
					Config: configFromPlan(schema, plan),
					State:  vnfStateFromModel(t, schema, baseModel),
				}, &updateResponse)
				requireDiagError(t, "update with an API failure", updateResponse.Diagnostics)
			})

			if test.parent {
				t.Run("update reports a label error", func(t *testing.T) {
					backend := newFakeVNFBackend()
					backend.seed()
					client := backend.client()
					client.Labels = &failingLabelClient{setErr: errVNFBoom}
					value := test.newValue(client)
					planModel := withModelAttribute(t, baseModel, "id", types.Int64Value(7))
					plan := vnfPlanFromModel(t, schema, planModel)
					updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
					value.Update(ctx, resource.UpdateRequest{
						Plan:   plan,
						Config: configFromPlan(schema, plan),
						State:  vnfStateFromModel(t, schema, planModel),
					}, &updateResponse)
					requireDiagError(t, "update with a label failure", updateResponse.Diagnostics)
				})
			}

			t.Run("delete rejects an undecodable state", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				deleteResponse := resource.DeleteResponse{State: tfsdk.State{Schema: schema}}
				value.Delete(ctx, resource.DeleteRequest{State: tfsdk.State{Schema: schema}}, &deleteResponse)
				requireDiagError(t, "delete with a null state", deleteResponse.Diagnostics)
			})

			t.Run("delete requires a configured client", func(t *testing.T) {
				value := test.newValue(nil)
				state := vnfStateFromModel(t, schema, baseModel)
				deleteResponse := resource.DeleteResponse{State: state}
				value.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
				requireDiagError(t, "delete without a client", deleteResponse.Diagnostics)
			})

			t.Run("delete reports the API error", func(t *testing.T) {
				value := test.newValue(failingVNFClient())
				state := vnfStateFromModel(t, schema, baseModel)
				deleteResponse := resource.DeleteResponse{State: state}
				value.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
				requireDiagError(t, "delete with an API failure", deleteResponse.Diagnostics)
			})

			t.Run("delete ignores an absent resource", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				state := vnfStateFromModel(t, schema, baseModel)
				deleteResponse := resource.DeleteResponse{State: state}
				value.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
				if deleteResponse.Diagnostics.HasError() {
					t.Fatalf("delete of an absent resource: %v", deleteResponse.Diagnostics)
				}
			})

			t.Run("import rejects an invalid id", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				importResponse := resource.ImportStateResponse{State: vnfStateFromModel(t, schema, baseModel)}
				importer, ok := value.(resource.ResourceWithImportState)
				if !ok {
					t.Fatalf("%T does not support import", value)
				}
				importer.ImportState(ctx, resource.ImportStateRequest{ID: "not-an-id"}, &importResponse)
				requireDiagError(t, "import with an invalid id", importResponse.Diagnostics)
			})
		})
	}
}

// vnfDataSourceErrorCase wires one parent data source into the shared
// error-path matrix.
type vnfDataSourceErrorCase struct {
	name     string
	newValue func(*Client) datasource.DataSource
	model    func() any
}

func vnfDataSourceErrorCases() []vnfDataSourceErrorCase {
	return []vnfDataSourceErrorCase{
		{
			name: "dhcp vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &dhcpVNFDataSource{configuredDataSource{client: client}}
			},
			model: func() any { return dhcpVNFModel{Labels: types.SetNull(types.StringType)} },
		},
		{
			name: "dns vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &dnsVNFDataSource{configuredDataSource{client: client}}
			},
			model: func() any { return dnsVNFModel{Labels: types.SetNull(types.StringType)} },
		},
		{
			name: "nat44 vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &nat44VNFDataSource{configuredDataSource{client: client}}
			},
			model: func() any { return nat44VNFModel{Labels: types.SetNull(types.StringType)} },
		},
		{
			name: "bgp vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &bgpVNFDataSource{configuredDataSource{client: client}}
			},
			model: func() any { return bgpVNFDataModel{Labels: types.SetNull(types.StringType)} },
		},
	}
}

// dataSourceConfig builds a data source config from a typed model.
func dataSourceConfig(t *testing.T, schema dschema.Schema, model any) tfsdk.Config {
	t.Helper()
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}
	return tfsdk.Config{Schema: schema, Raw: plan.Raw}
}

// TestVNFDataSourceErrorPaths covers the data source failure branches: an
// undecodable config, an unconfigured client, an API error, and a label error.
// Together with TestVNFDataSourceRead this also pins the neither-set rejection.
func TestVNFDataSourceErrorPaths(t *testing.T) {
	ctx := context.Background()
	for _, test := range vnfDataSourceErrorCases() {
		t.Run(test.name, func(t *testing.T) {
			healthy := test.newValue(newFakeVNFBackend().client())
			var schemaResponse datasource.SchemaResponse
			healthy.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
			if schemaResponse.Diagnostics.HasError() {
				t.Fatalf("schema: %v", schemaResponse.Diagnostics)
			}
			schema := schemaResponse.Schema
			baseModel := test.model()

			t.Run("read rejects an undecodable config", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				readResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
				value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema}}, &readResponse)
				requireDiagError(t, "read with a null config", readResponse.Diagnostics)
			})

			t.Run("read requires a configured client", func(t *testing.T) {
				value := test.newValue(nil)
				config := dataSourceConfig(t, schema, withModelAttribute(t, baseModel, "id", types.Int64Value(7)))
				readResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
				value.Read(ctx, datasource.ReadRequest{Config: config}, &readResponse)
				requireDiagError(t, "read without a client", readResponse.Diagnostics)
			})

			t.Run("read rejects neither id nor name", func(t *testing.T) {
				value := test.newValue(newFakeVNFBackend().client())
				config := dataSourceConfig(t, schema, baseModel)
				readResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
				value.Read(ctx, datasource.ReadRequest{Config: config}, &readResponse)
				requireDiagError(t, "read without id or name", readResponse.Diagnostics)
			})

			t.Run("read reports the by-id API error", func(t *testing.T) {
				value := test.newValue(failingVNFClient())
				config := dataSourceConfig(t, schema, withModelAttribute(t, baseModel, "id", types.Int64Value(7)))
				readResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
				value.Read(ctx, datasource.ReadRequest{Config: config}, &readResponse)
				requireDiagError(t, "read by id with an API failure", readResponse.Diagnostics)
			})

			t.Run("read reports the by-name list error", func(t *testing.T) {
				value := test.newValue(failingVNFClient())
				config := dataSourceConfig(t, schema, withModelAttribute(t, baseModel, "name", types.StringValue("app")))
				readResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
				value.Read(ctx, datasource.ReadRequest{Config: config}, &readResponse)
				requireDiagError(t, "read by name with an API failure", readResponse.Diagnostics)
			})

			t.Run("read reports a label error", func(t *testing.T) {
				backend := newFakeVNFBackend()
				backend.seed()
				client := backend.client()
				client.Labels = &failingLabelClient{getErr: errVNFBoom}
				value := test.newValue(client)
				config := dataSourceConfig(t, schema, withModelAttribute(t, baseModel, "id", types.Int64Value(7)))
				readResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
				value.Read(ctx, datasource.ReadRequest{Config: config}, &readResponse)
				requireDiagError(t, "read with a label failure", readResponse.Diagnostics)
			})
		})
	}
}

// TestSensitiveValueBranches pins the write-only value mapping: null and
// unknown inputs become null, while a configured value survives.
func TestSensitiveValueBranches(t *testing.T) {
	tests := []struct {
		name      string
		value     types.String
		wantNull  bool
		wantValue string
	}{
		{name: "null", value: types.StringNull(), wantNull: true},
		{name: "unknown", value: types.StringUnknown(), wantNull: true},
		{name: "configured", value: types.StringValue("secret"), wantValue: "secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sensitiveValue(test.value)
			if test.wantNull {
				if !got.IsNull() {
					t.Fatalf("sensitiveValue(%v) = %v, want null", test.value, got)
				}
				return
			}
			if got.IsNull() || got.ValueString() != test.wantValue {
				t.Fatalf("sensitiveValue(%v) = %v, want %q", test.value, got, test.wantValue)
			}
		})
	}
}

// TestFindVNFByNameBranches covers the case-insensitive single match, no match,
// and duplicate match paths of the shared name resolver.
func TestFindVNFByNameBranches(t *testing.T) {
	getName := func(value string) string { return value }

	got, err := findVNFByName([]string{"app-dhcp"}, "APP-DHCP", getName)
	if err != nil || got != "app-dhcp" {
		t.Fatalf("case-insensitive lookup = %q, %v", got, err)
	}
	if _, err := findVNFByName([]string{"app-dhcp"}, "missing", getName); err == nil {
		t.Fatal("missing name accepted")
	}
	if _, err := findVNFByName([]string{"app-dhcp", "APP-DHCP"}, "app-dhcp", getName); err == nil {
		t.Fatal("duplicate name accepted")
	}
}

// TestVNFImportRejectsMalformedIDs pins both import-id parsers: parent
// resources take one integer, children take <vnf_id>:<child_id>.
func TestVNFImportRejectsMalformedIDs(t *testing.T) {
	ctx := context.Background()
	parentCases := []struct {
		name  string
		model func() any
		value resource.Resource
	}{
		{"dhcp vnf", func() any { return testDHCPVNFModel() }, &dhcpVNFResource{}},
		{"dns vnf", func() any { return testDNSVNFModel() }, &dnsVNFResource{}},
		{"nat44 vnf", func() any { return testNAT44VNFModel() }, &nat44VNFResource{}},
		{"bgp vnf", func() any { return testBGPVNFModel() }, &bgpVNFResource{}},
	}
	for _, test := range parentCases {
		t.Run(test.name+" parent id", func(t *testing.T) {
			value := test.value
			schema := resourceSchema(t, value)
			importResponse := resource.ImportStateResponse{State: vnfStateFromModel(t, schema, test.model())}
			importer := value.(resource.ResourceWithImportState)
			importer.ImportState(ctx, resource.ImportStateRequest{ID: "0"}, &importResponse)
			requireDiagError(t, "import with a zero id", importResponse.Diagnostics)
		})
	}

	childCases := []struct {
		name  string
		model func() any
		value resource.Resource
	}{
		{"dhcp static assignment", func() any { return testDHCPStaticAssignmentModel() }, &dhcpStaticAssignmentResource{}},
		{"dhcp option", func() any { return testDHCPOptionModel() }, &dhcpOptionResource{}},
		{"dns record", func() any { return testDNSRecordModel() }, &dnsRecordResource{}},
		{"dns forwarder", func() any { return testDNSForwarderModel() }, &dnsForwarderResource{}},
		{"nat44 dnat rule", func() any { return testNAT44DNATRuleModel() }, &nat44DNATRuleResource{}},
		{"bgp advertised prefix", func() any { return testBGPAdvertisedPrefixModel() }, &bgpAdvertisedPrefixResource{}},
	}
	for _, test := range childCases {
		t.Run(test.name+" malformed child id", func(t *testing.T) {
			value := test.value
			schema := resourceSchema(t, value)
			importer := value.(resource.ResourceWithImportState)
			for _, importID := range []string{"not-an-id", "7", "7:not-an-id", "0:1", "7:0", "7:1:2"} {
				importResponse := resource.ImportStateResponse{State: vnfStateFromModel(t, schema, test.model())}
				importer.ImportState(ctx, resource.ImportStateRequest{ID: importID}, &importResponse)
				if !importResponse.Diagnostics.HasError() {
					t.Fatalf("import id %q was accepted", importID)
				}
			}
		})
	}
}

// TestVNFSensitiveAttributes pins the declared sensitive attributes and the BGP
// write-only password behavior: a configured password survives create, while a
// null or unresolved password is stored as null.
func TestVNFSensitiveAttributes(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		value     resource.Resource
		attribute string
	}{
		{"dhcp option value", &dhcpOptionResource{}, "value"},
		{"dns record value", &dnsRecordResource{}, "value"},
		{"bgp password", &bgpVNFResource{}, "password"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			schema := resourceSchema(t, test.value)
			attr, ok := schema.Attributes[test.attribute]
			if !ok {
				t.Fatalf("attribute %q is missing", test.attribute)
			}
			if !attr.IsSensitive() {
				t.Fatalf("%q is not sensitive", test.attribute)
			}
		})
	}

	for _, password := range []types.String{types.StringNull(), types.StringUnknown()} {
		t.Run("bgp password "+password.String(), func(t *testing.T) {
			backend := newFakeVNFBackend()
			value := &bgpVNFResource{configuredResource{client: backend.client()}}
			schema := resourceSchema(t, value)
			model := testBGPVNFModel()
			model.Password = password
			plan := vnfPlanFromModel(t, schema, model)
			createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
			value.Create(ctx, resource.CreateRequest{Plan: plan, Config: configFromPlan(schema, plan)}, &createResponse)
			if createResponse.Diagnostics.HasError() {
				t.Fatalf("create: %v", createResponse.Diagnostics)
			}
			var created bgpVNFModel
			if diags := createResponse.State.Get(ctx, &created); diags.HasError() {
				t.Fatalf("created state: %v", diags)
			}
			if !created.Password.IsNull() {
				t.Fatalf("password = %v, want null", created.Password)
			}
		})
	}
}
