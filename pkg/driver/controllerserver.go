package driver

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/pborman/uuid"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/MooreThreads/csi-driver-3fs/pkg/state"
)

const (
	deviceID = "deviceID"
)

func (c3 *csi3fs) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (resp *csi.CreateVolumeResponse, finalErr error) {
	if err := c3.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.V(3).Infof("invalid create volume req: %v", req)
		return nil, err
	}

	if len(req.GetMutableParameters()) > 0 {
		if err := c3.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_MODIFY_VOLUME); err != nil {
			klog.V(3).Infof("invalid create volume req: %v", req)
			return nil, err
		}

		if err := c3.validateVolumeMutableParameters(req.MutableParameters); err != nil {
			return nil, err
		}
	}

	// Check arguments
	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}
	caps := req.GetVolumeCapabilities()
	if caps == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}

	requestedAccessType := state.MountAccess

	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	capacity := int64(req.GetCapacityRange().GetRequiredBytes())

	volumeID := uuid.NewUUID().String()
	kind := req.GetParameters()[storageKind]
	vol, err := c3.createVolume(volumeID, req.GetName(), capacity, requestedAccessType, false /* ephemeral */, kind)
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("created volume %s at path %s", vol.VolID, vol.VolPath)

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeID,
			CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
			VolumeContext: req.GetParameters(),
			ContentSource: req.GetVolumeContentSource(),
		},
	}, nil
}

func (c3 *csi3fs) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if err := c3.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.V(3).Infof("invalid delete volume req: %v", req)
		return nil, err
	}

	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	volId := req.GetVolumeId()
	vol, err := c3.state.GetVolumeByID(volId)
	if err != nil {
		return &csi.DeleteVolumeResponse{}, nil
	}

	if vol.Attached || !vol.Published.Empty() || !vol.Staged.Empty() {
		msg := fmt.Sprintf("Volume '%s' is still used (attached: %v, staged: %v, published: %v) by '%s' node",
			vol.VolID,
			vol.Attached,
			vol.Staged,
			vol.Published,
			vol.NodeID,
		)
		if c3.config.CheckVolumeLifecycle {
			return nil, status.Error(codes.Internal, msg)
		}
		klog.Warning(msg)
	}

	if err := c3.deleteVolume(volId); err != nil {
		return nil, fmt.Errorf("failed to delete volume %v: %w", volId, err)
	}

	klog.V(4).Infof("volume %v successfully deleted", volId)
	return &csi.DeleteVolumeResponse{}, nil
}

func (c3 *csi3fs) ControllerGetCapabilities(
	ctx context.Context,
	req *csi.ControllerGetCapabilitiesRequest,
) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: c3.getControllerServiceCapabilities()}, nil
}

func (c3 *csi3fs) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest,
) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.VolumeCapabilities) == 0 {
		return nil, status.Error(codes.InvalidArgument, req.VolumeId)
	}

	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	if _, err := c3.state.GetVolumeByID(req.GetVolumeId()); err != nil {
		return nil, err
	}

	for _, cap := range req.GetVolumeCapabilities() {
		if cap.GetMount() == nil {
			return nil, status.Error(codes.InvalidArgument, "cannot have mount access type be undefined")
		}
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil
}

func (c3 *csi3fs) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest,
) (*csi.ControllerPublishVolumeResponse, error) {
	if !c3.config.EnableAttach {
		return nil, status.Error(codes.Unimplemented, "ControllerPublishVolume is not supported")
	}

	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.NodeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Node ID cannot be empty")
	}
	if req.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities cannot be empty")
	}

	if req.NodeId != c3.config.NodeID {
		return nil, status.Errorf(codes.NotFound, "Not matching Node ID %s to 3fs Node ID %s", req.NodeId, c3.config.NodeID)
	}

	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	vol, err := c3.state.GetVolumeByID(req.VolumeId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	// Check to see if the volume is already published.
	if vol.Attached {
		if req.GetReadonly() != vol.ReadOnlyAttach {
			return nil, status.Error(codes.AlreadyExists, "Volume published but has incompatible readonly flag")
		}

		return &csi.ControllerPublishVolumeResponse{PublishContext: map[string]string{}}, nil
	}

	// Check attach limit before publishing.
	if c3.config.AttachLimit > 0 && c3.getAttachCount() >= c3.config.AttachLimit {
		return nil, status.Errorf(codes.ResourceExhausted, "Cannot attach any more volumes to this node ('%s')", c3.config.NodeID)
	}

	vol.Attached = true
	vol.ReadOnlyAttach = req.GetReadonly()
	if err := c3.state.UpdateVolume(vol); err != nil {
		return nil, err
	}

	return &csi.ControllerPublishVolumeResponse{PublishContext: map[string]string{}}, nil
}

func (c3 *csi3fs) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest,
) (*csi.ControllerUnpublishVolumeResponse, error) {
	if !c3.config.EnableAttach {
		return nil, status.Error(codes.Unimplemented, "ControllerUnpublishVolume is not supported")
	}

	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}

	// Empty node id is not a failure as per Spec
	if req.NodeId != "" && req.NodeId != c3.config.NodeID {
		return nil, status.Errorf(codes.NotFound, "Node ID %s does not match to expected Node ID %s", req.NodeId, c3.config.NodeID)
	}

	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	vol, err := c3.state.GetVolumeByID(req.VolumeId)
	if err != nil {
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	// Check to see if the volume is staged/published on a node
	if !vol.Published.Empty() || !vol.Staged.Empty() {
		msg := fmt.Sprintf("Volume '%s' is still used (staged: %v, published: %v) by '%s' node",
			vol.VolID,
			vol.Staged,
			vol.Published,
			vol.NodeID,
		)
		if c3.config.CheckVolumeLifecycle {
			return nil, status.Error(codes.Internal, msg)
		}
		klog.Warning(msg)
	}

	vol.Attached = false
	if err := c3.state.UpdateVolume(vol); err != nil {
		return nil, status.Errorf(codes.Internal, "could not update volume %s: %v", vol.VolID, err)
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (c3 *csi3fs) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	available := c3.config.MaxVolumeSize
	if c3.config.Capacity.Enabled() {
		kind := req.GetParameters()[storageKind]
		quantity := c3.config.Capacity[kind]
		allocated := c3.sumVolumeSizes(kind)
		available = quantity.Value() - allocated
	}
	maxVolumeSize := c3.config.MaxVolumeSize
	if maxVolumeSize > available {
		maxVolumeSize = available
	}

	return &csi.GetCapacityResponse{
		AvailableCapacity: available,
		MaximumVolumeSize: &wrapperspb.Int64Value{Value: maxVolumeSize},
		MinimumVolumeSize: &wrapperspb.Int64Value{Value: 0},
	}, nil
}

func (c3 *csi3fs) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	volumeRes := &csi.ListVolumesResponse{
		Entries: []*csi.ListVolumesResponse_Entry{},
	}

	var (
		startIdx, volumesLength, maxLength int64
		c3Volume                           state.Volume
	)
	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	// Sort by volume ID.
	volumes := c3.state.GetVolumes()
	sort.Slice(volumes, func(i, j int) bool {
		return volumes[i].VolID < volumes[j].VolID
	})

	if req.StartingToken == "" {
		req.StartingToken = "1"
	}

	startIdx, err := strconv.ParseInt(req.StartingToken, 10, 32)
	if err != nil {
		return nil, status.Error(codes.Aborted, "The type of startingToken should be integer")
	}

	volumesLength = int64(len(volumes))
	maxLength = int64(req.MaxEntries)

	if maxLength > volumesLength || maxLength <= 0 {
		maxLength = volumesLength
	}

	for index := startIdx - 1; index < volumesLength && index < maxLength; index++ {
		c3Volume = volumes[index]
		healthy, msg := c3.doHealthCheckInControllerSide(c3Volume.VolID)
		klog.V(3).Infof("Healthy state: %s Volume: %t", c3Volume.VolName, healthy)
		volumeRes.Entries = append(volumeRes.Entries, &csi.ListVolumesResponse_Entry{
			Volume: &csi.Volume{
				VolumeId:      c3Volume.VolID,
				CapacityBytes: c3Volume.VolSize,
			},
			Status: &csi.ListVolumesResponse_VolumeStatus{
				PublishedNodeIds: []string{c3Volume.NodeID},
				VolumeCondition: &csi.VolumeCondition{
					Abnormal: !healthy,
					Message:  msg,
				},
			},
		})
	}

	klog.V(5).Infof("Volumes are: %+v", volumeRes)
	return volumeRes, nil
}

func (c3 *csi3fs) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	var abnormal bool
	var err error

	volume, err := c3.state.GetVolumeByID(req.GetVolumeId())
	if err != nil {
		abnormal = true
	}

	return &csi.ControllerGetVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volume.VolID,
			CapacityBytes: volume.VolSize,
		},
		Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
			PublishedNodeIds: []string{volume.NodeID},
			VolumeCondition: &csi.VolumeCondition{
				Abnormal: abnormal,
				Message:  err.Error(),
			},
		},
	}, nil
}

func (c3 *csi3fs) ControllerModifyVolume(ctx context.Context, req *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	if err := c3.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_MODIFY_VOLUME); err != nil {
		return nil, err
	}

	// Check arguments
	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.MutableParameters) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Mutable parameters cannot be empty")
	}

	c3.mutex.Lock()
	defer c3.mutex.Unlock()

	_, err := c3.state.GetVolumeByID(req.VolumeId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	return &csi.ControllerModifyVolumeResponse{}, nil
}

// validateVolumeMutableParameters is a helper function to check if the mutable parameters are in the accepted list
func (c3 *csi3fs) validateVolumeMutableParameters(params map[string]string) error {
	if len(c3.config.AcceptedMutableParameterNames) == 0 {
		return nil
	}

	accepts := sets.New(c3.config.AcceptedMutableParameterNames...)
	unsupported := []string{}
	for k := range params {
		if !accepts.Has(k) {
			unsupported = append(unsupported, k)
		}
	}
	if len(unsupported) > 0 {
		return status.Errorf(codes.InvalidArgument, "invalid parameters: %v", unsupported)
	}
	return nil
}

func (c3 *csi3fs) validateControllerServiceRequest(c csi.ControllerServiceCapability_RPC_Type) error {
	if c == csi.ControllerServiceCapability_RPC_UNKNOWN {
		return nil
	}

	for _, cap := range c3.getControllerServiceCapabilities() {
		if c == cap.GetRpc().GetType() {
			return nil
		}
	}
	return status.Errorf(codes.InvalidArgument, "unsupported capability %s", c)
}

func (c3 *csi3fs) getControllerServiceCapabilities() []*csi.ControllerServiceCapability {
	cl := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		csi.ControllerServiceCapability_RPC_VOLUME_CONDITION,
		csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}
	if c3.config.EnableVolumeExpansion && !c3.config.DisableControllerExpansion {
		cl = append(cl, csi.ControllerServiceCapability_RPC_EXPAND_VOLUME)
	}
	if c3.config.EnableAttach {
		cl = append(cl, csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME)
	}
	if c3.config.EnableControllerModifyVolume {
		cl = append(cl, csi.ControllerServiceCapability_RPC_MODIFY_VOLUME)
	}

	csc := make([]*csi.ControllerServiceCapability, 0, len(cl))

	for _, cap := range cl {
		csc = append(csc, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}

	return csc
}
