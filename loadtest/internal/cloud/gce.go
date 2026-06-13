package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/proto"
)

// GCEProvisioner is the live Provisioner backed by the Compute and Storage
// SDKs. Resource names are derived from the run id; everything is labelled for
// cost reporting and reaping. Teardown is best-effort: a missing resource never
// fails the whole operation.
type GCEProvisioner struct {
	project  string
	location string // GCS bucket location (multi-region)
	log      *slog.Logger

	instances   *compute.InstancesClient
	networks    *compute.NetworksClient
	subnetworks *compute.SubnetworksClient
	firewalls   *compute.FirewallsClient
	storage     *storage.Client
}

// NewGCEProvisioner constructs the SDK clients using Application Default
// Credentials. Call Close to release them.
func NewGCEProvisioner(ctx context.Context, project string, log *slog.Logger) (*GCEProvisioner, error) {
	if project == "" {
		return nil, errors.New("project is required for live provisioning")
	}
	inst, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	nets, err := compute.NewNetworksRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	subs, err := compute.NewSubnetworksRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	fws, err := compute.NewFirewallsRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &GCEProvisioner{
		project:     project,
		location:    "US",
		log:         log,
		instances:   inst,
		networks:    nets,
		subnetworks: subs,
		firewalls:   fws,
		storage:     st,
	}, nil
}

// Close releases the SDK clients.
func (g *GCEProvisioner) Close() error {
	return errors.Join(
		g.instances.Close(), g.networks.Close(),
		g.subnetworks.Close(), g.firewalls.Close(), g.storage.Close(),
	)
}

// Resource naming, all derived from the run id.
func netName(runID string) string            { return "mclt-" + runID + "-net" }
func fwName(runID string) string             { return "mclt-" + runID + "-fw" }
func subnetName(runID, region string) string { return "mclt-" + runID + "-" + region }

func (g *GCEProvisioner) EnsureNetwork(ctx context.Context, runID string, regions []string) error {
	op, err := g.networks.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: g.project,
		NetworkResource: &computepb.Network{
			Name:                  proto.String(netName(runID)),
			AutoCreateSubnetworks: proto.Bool(false),
		},
	})
	if err := waitInsert(ctx, op, err); err != nil {
		return fmt.Errorf("create network: %w", err)
	}

	for i, region := range regions {
		cidr := fmt.Sprintf("10.%d.0.0/16", 8+i)
		op, err := g.subnetworks.Insert(ctx, &computepb.InsertSubnetworkRequest{
			Project: g.project,
			Region:  region,
			SubnetworkResource: &computepb.Subnetwork{
				Name:        proto.String(subnetName(runID, region)),
				Network:     proto.String("global/networks/" + netName(runID)),
				IpCidrRange: proto.String(cidr),
				Region:      proto.String(region),
			},
		})
		if err := waitInsert(ctx, op, err); err != nil {
			return fmt.Errorf("create subnet %s: %w", region, err)
		}
	}
	return nil
}

func (g *GCEProvisioner) EnsureFirewall(ctx context.Context, runID string) error {
	op, err := g.firewalls.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: g.project,
		FirewallResource: &computepb.Firewall{
			Name:       proto.String(fwName(runID)),
			Network:    proto.String("global/networks/" + netName(runID)),
			Direction:  proto.String("INGRESS"),
			Allowed:    []*computepb.Allowed{{IPProtocol: proto.String("tcp"), Ports: []string{"11211"}}},
			SourceTags: []string{string(RoleClient)},
			TargetTags: []string{string(RoleServer)},
		},
	})
	return waitInsert(ctx, op, err)
}

func (g *GCEProvisioner) UploadBinaries(ctx context.Context, bucketURI string, bins map[string]string) error {
	bucket := parseGSBucket(bucketURI)
	if err := g.ensureBucket(ctx, bucket); err != nil {
		return err
	}
	for name, path := range bins {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		w := g.storage.Bucket(bucket).Object("bin/" + name).NewWriter(ctx)
		_, copyErr := io.Copy(w, f)
		f.Close()
		if copyErr != nil {
			_ = w.Close()
			return fmt.Errorf("upload %s: %w", name, copyErr)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("finalize %s: %w", name, err)
		}
		g.log.Info("uploaded binary", "name", name, "bucket", bucket)
	}
	return nil
}

func (g *GCEProvisioner) ensureBucket(ctx context.Context, bucket string) error {
	b := g.storage.Bucket(bucket)
	if _, err := b.Attrs(ctx); err == nil {
		return nil
	}
	err := b.Create(ctx, g.project, &storage.BucketAttrs{
		Location: g.location,
		Labels:   map[string]string{"app": AppLabel},
	})
	if isAlreadyExists(err) {
		return nil
	}
	return err
}

func (g *GCEProvisioner) CreateVM(ctx context.Context, vm PlannedVM) (string, error) {
	runID := vm.Labels["run-id"]
	region := RegionOf(vm.Zone)

	scheduling := &computepb.Scheduling{}
	if vm.Spot {
		scheduling.ProvisioningModel = proto.String("SPOT")
		scheduling.InstanceTerminationAction = proto.String("DELETE")
		scheduling.AutomaticRestart = proto.Bool(false)
	}

	op, err := g.instances.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: g.project,
		Zone:    vm.Zone,
		InstanceResource: &computepb.Instance{
			Name:        proto.String(vm.Name),
			MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", vm.Zone, vm.MachineType)),
			Disks: []*computepb.AttachedDisk{{
				Boot:       proto.Bool(true),
				AutoDelete: proto.Bool(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: proto.String("projects/debian-cloud/global/images/family/debian-12"),
					DiskSizeGb:  proto.Int64(20),
				},
			}},
			NetworkInterfaces: []*computepb.NetworkInterface{{
				Subnetwork: proto.String(fmt.Sprintf("regions/%s/subnetworks/%s", region, subnetName(runID, region))),
				// Ephemeral external IP for egress (GCS) and SSH; the memcache
				// port is never opened externally (firewall is tag-scoped).
				AccessConfigs: []*computepb.AccessConfig{{
					Name: proto.String("External NAT"),
					Type: proto.String("ONE_TO_ONE_NAT"),
				}},
			}},
			Labels: vm.Labels,
			Tags:   &computepb.Tags{Items: []string{string(vm.Role)}},
			// Attach the default compute service account with a storage scope so
			// the startup-script can pull binaries from and push artifacts to GCS
			// via the metadata token. Omitting this leaves the VM with no service
			// account, and every GCS call fails.
			ServiceAccounts: []*computepb.ServiceAccount{{
				Email:  proto.String("default"),
				Scopes: []string{"https://www.googleapis.com/auth/devstorage.read_write"},
			}},
			Scheduling: scheduling,
			Metadata: &computepb.Metadata{
				Items: []*computepb.Items{{
					Key:   proto.String("startup-script"),
					Value: proto.String(vm.StartupScript),
				}},
			},
		},
	})
	if err := waitInsert(ctx, op, err); err != nil {
		return "", fmt.Errorf("create instance %s: %w", vm.Name, err)
	}

	inst, err := g.instances.Get(ctx, &computepb.GetInstanceRequest{
		Project: g.project, Zone: vm.Zone, Instance: vm.Name,
	})
	if err != nil {
		return "", fmt.Errorf("get instance %s: %w", vm.Name, err)
	}
	nics := inst.GetNetworkInterfaces()
	if len(nics) == 0 {
		return "", fmt.Errorf("instance %s has no network interface", vm.Name)
	}
	return nics[0].GetNetworkIP(), nil
}

func (g *GCEProvisioner) CollectLogs(ctx context.Context, bucketURI, runID, localDir string) error {
	bucket := parseGSBucket(bucketURI)
	it := g.storage.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: runID + "/"})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return err
		}
		if err := g.downloadObject(ctx, bucket, attrs.Name, localDir); err != nil {
			g.log.Error("download failed", "object", attrs.Name, "err", err)
		}
	}
	return nil
}

func (g *GCEProvisioner) downloadObject(ctx context.Context, bucket, name, localDir string) error {
	r, err := g.storage.Bucket(bucket).Object(name).NewReader(ctx)
	if err != nil {
		return err
	}
	defer r.Close()
	dst := filepath.Join(localDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (g *GCEProvisioner) DeleteByRun(ctx context.Context, runID string) error {
	g.deleteInstancesByFilter(ctx, fmt.Sprintf("labels.run-id=%s", runID))

	// firewall, subnets, then the network (in dependency order); ignore missing.
	if op, err := g.firewalls.Delete(ctx, &computepb.DeleteFirewallRequest{
		Project: g.project, Firewall: fwName(runID),
	}); !ignoreMissing(waitDelete(ctx, op, err)) {
		g.log.Error("delete firewall", "run", runID)
	}
	g.deleteSubnetsByPrefix(ctx, "mclt-"+runID+"-")
	if op, err := g.networks.Delete(ctx, &computepb.DeleteNetworkRequest{
		Project: g.project, Network: netName(runID),
	}); !ignoreMissing(waitDelete(ctx, op, err)) {
		g.log.Error("delete network", "run", runID)
	}
	return nil
}

func (g *GCEProvisioner) Reap(ctx context.Context, ttlHours int) ([]string, error) {
	cutoff := time.Now().Add(-time.Duration(ttlHours) * time.Hour).Unix()
	var deleted []string
	it := g.instances.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{
		Project: g.project,
		Filter:  proto.String(fmt.Sprintf("labels.app=%s", AppLabel)),
	})
	for {
		pair, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return deleted, err
		}
		zone := lastSegment(pair.Key)
		for _, inst := range pair.Value.GetInstances() {
			created, _ := strconv.ParseInt(inst.GetLabels()["created"], 10, 64)
			if created != 0 && created >= cutoff {
				continue // still within its TTL
			}
			name := inst.GetName()
			op, err := g.instances.Delete(ctx, &computepb.DeleteInstanceRequest{
				Project: g.project, Zone: zone, Instance: name,
			})
			if err := waitDelete(ctx, op, err); err == nil {
				deleted = append(deleted, name)
			}
		}
	}
	return deleted, nil
}

func (g *GCEProvisioner) deleteInstancesByFilter(ctx context.Context, filter string) {
	it := g.instances.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{
		Project: g.project, Filter: proto.String(filter),
	})
	for {
		pair, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			g.log.Error("list instances", "err", err)
			return
		}
		zone := lastSegment(pair.Key)
		for _, inst := range pair.Value.GetInstances() {
			op, err := g.instances.Delete(ctx, &computepb.DeleteInstanceRequest{
				Project: g.project, Zone: zone, Instance: inst.GetName(),
			})
			if err := waitDelete(ctx, op, err); err != nil {
				g.log.Error("delete instance", "name", inst.GetName(), "err", err)
			}
		}
	}
}

func (g *GCEProvisioner) deleteSubnetsByPrefix(ctx context.Context, prefix string) {
	it := g.subnetworks.AggregatedList(ctx, &computepb.AggregatedListSubnetworksRequest{
		Project: g.project,
	})
	for {
		pair, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return
		}
		region := lastSegment(pair.Key)
		for _, sn := range pair.Value.GetSubnetworks() {
			if !strings.HasPrefix(sn.GetName(), prefix) {
				continue
			}
			op, err := g.subnetworks.Delete(ctx, &computepb.DeleteSubnetworkRequest{
				Project: g.project, Region: region, Subnetwork: sn.GetName(),
			})
			_ = waitDelete(ctx, op, err)
		}
	}
}

// --- helpers ---

// waitInsert waits for a create operation, treating "already exists" as success
// (re-runs and shared resources are idempotent).
func waitInsert(ctx context.Context, op *compute.Operation, err error) error {
	if isAlreadyExists(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if werr := op.Wait(ctx); werr != nil && !isAlreadyExists(werr) {
		return werr
	}
	return nil
}

func waitDelete(ctx context.Context, op *compute.Operation, err error) error {
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

func ignoreMissing(err error) bool { return err == nil || isNotFound(err) }

func isAlreadyExists(err error) bool { return gapiCode(err) == 409 }
func isNotFound(err error) bool      { return gapiCode(err) == 404 }

func gapiCode(err error) int {
	var ae *googleapi.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 0
}

// parseGSBucket extracts the bucket name from a gs://bucket[/prefix] URI.
func parseGSBucket(uri string) string {
	s := strings.TrimPrefix(uri, "gs://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}

// lastSegment returns the trailing path element of an aggregated-list key like
// "zones/us-central1-a" -> "us-central1-a".
func lastSegment(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// compile-time assertion that GCEProvisioner satisfies the interface.
var _ Provisioner = (*GCEProvisioner)(nil)
