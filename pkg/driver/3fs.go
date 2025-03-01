package driver

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"

	"github.com/MooreThreads/csi-driver-3fs/pkg/state"
)

const (
	storageKind = "kind"
)

type csi3fs struct {
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer
	csi.UnimplementedNodeServer
	csi.UnimplementedGroupControllerServer
	csi.UnimplementedSnapshotMetadataServer

	config Config
	mutex  sync.Mutex
	state  state.State
}

type Config struct {
	DriverName                    string
	Endpoint                      string
	ProxyEndpoint                 string
	NodeID                        string
	VendorVersion                 string
	StateDir                      string
	MaxVolumesPerNode             int64
	MaxVolumeSize                 int64
	AttachLimit                   int64
	Capacity                      Capacity
	Ephemeral                     bool
	ShowVersion                   bool
	EnableAttach                  bool
	EnableVolumeExpansion         bool
	EnableControllerModifyVolume  bool
	AcceptedMutableParameterNames StringArray
	DisableControllerExpansion    bool
	DisableNodeExpansion          bool
	MaxVolumeExpansionSizeNode    int64
	CheckVolumeLifecycle          bool
}

func NewCsi3fsDriver(cfg Config) (*csi3fs, error) {
	if cfg.DriverName == "" {
		return nil, errors.New("no driver name provided")
	}

	if cfg.NodeID == "" {
		return nil, errors.New("no node id provided")
	}

	if cfg.Endpoint == "" {
		return nil, errors.New("no driver endpoint provided")
	}

	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create dataRoot: %v", err)
	}

	klog.Infof("Driver: %v ", cfg.DriverName)
	klog.Infof("Version: %s", cfg.VendorVersion)

	s, err := state.New(path.Join(cfg.StateDir, "state.json"))
	if err != nil {
		return nil, err
	}

	c3 := &csi3fs{config: cfg, state: s}
	return c3, nil
}

func (c3 *csi3fs) Run() error {
	s := NewNonBlockingGRPCServer()
	s.Start(c3.config.Endpoint, c3, c3, c3)
	s.Wait()

	return nil
}

func (c3 *csi3fs) getVolumePath(volID string) string {
	return filepath.Join(c3.config.StateDir, volID)
}

// createVolume creates the directory for the 3fs volume.
func (c3 *csi3fs) createVolume(volID, name string, cap int64, volAccessType state.AccessType, ephemeral bool, kind string) (*state.Volume, error) {
	// Check for maximum available capacity
	if cap > c3.config.MaxVolumeSize {
		return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds maximum allowed %d", cap, c3.config.MaxVolumeSize)
	}

	if c3.config.Capacity.Enabled() {
		if kind == "" {
			// Pick some kind with sufficient remaining capacity.
			for k, c := range c3.config.Capacity {
				if c3.sumVolumeSizes(k)+cap <= c.Value() {
					kind = k
					break
				}
			}
		}
		if kind == "" {
			// Still nothing?!
			return nil, status.Errorf(codes.ResourceExhausted, "requested capacity %d of arbitrary storage exceeds all remaining capacity", cap)
		}
		used := c3.sumVolumeSizes(kind)
		available := c3.config.Capacity[kind]
		if used+cap > available.Value() {
			return nil, status.Errorf(codes.ResourceExhausted, "requested capacity %d exceeds remaining capacity for %q, %s out of %s already used",
				cap, kind, resource.NewQuantity(used, resource.BinarySI).String(), available.String())
		}
	} else if kind != "" {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("capacity tracking disabled, specifying kind %q is invalid", kind))
	}

	path := c3.getVolumePath(volID)

	switch volAccessType {
	case state.MountAccess:
		err := os.MkdirAll(path, 0o777)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported access type %v", volAccessType)
	}

	volume := state.Volume{
		VolID:         volID,
		VolName:       name,
		VolSize:       cap,
		VolPath:       path,
		VolAccessType: volAccessType,
		Kind:          kind,
	}

	klog.V(4).Infof("adding 3fs volume: %s = %+v", volID, volume)
	if err := c3.state.UpdateVolume(volume); err != nil {
		return nil, err
	}
	return &volume, nil
}

// deleteVolume deletes the directory for the 3fs volume.
func (c3 *csi3fs) deleteVolume(volID string) error {
	klog.V(4).Infof("starting to delete 3fs volume: %s", volID)

	vol, err := c3.state.GetVolumeByID(volID)
	if err != nil {
		return nil
	}

	path := c3.getVolumePath(volID)
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := c3.state.DeleteVolume(volID); err != nil {
		return err
	}

	klog.V(4).Infof("deleted 3fs volume: %s = %+v", volID, vol)
	return nil
}

func (c3 *csi3fs) sumVolumeSizes(kind string) int64 {
	var sum int64
	for _, volume := range c3.state.GetVolumes() {
		if volume.Kind == kind {
			sum += volume.VolSize
		}
	}
	return sum
}

func (c3 *csi3fs) getAttachCount() int64 {
	var count int64
	for _, vol := range c3.state.GetVolumes() {
		if vol.Attached {
			count++
		}
	}
	return count
}
