package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"k8s.io/klog/v2"
	fs "k8s.io/kubernetes/pkg/volume/util/fs"
)

const (
	podVolumeTargetPath       = "/var/lib/kubelet/pods"
	csiSignOfVolumeTargetPath = "kubernetes.io~csi/pvc"
)

type MountPointInfo struct {
	Target              string           `json:"target"`
	Source              string           `json:"source"`
	FsType              string           `json:"fstype"`
	Options             string           `json:"options"`
	ContainerFileSystem []MountPointInfo `json:"children,omitempty"`
}

type ContainerFileSystem struct {
	Children []MountPointInfo `json:"children"`
}

type FileSystems struct {
	Filsystem []ContainerFileSystem `json:"filesystems"`
}

func checkPathExist(path string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func parseMountInfo(originalMountInfo []byte) ([]MountPointInfo, error) {
	fs := FileSystems{
		Filsystem: make([]ContainerFileSystem, 0),
	}

	if err := json.Unmarshal(originalMountInfo, &fs); err != nil {
		return nil, err
	}

	if len(fs.Filsystem) <= 0 {
		return nil, fmt.Errorf("failed to get mount info")
	}

	return fs.Filsystem[0].Children, nil
}

func checkMountPointExist(volumePath string) (bool, error) {
	cmdPath, err := exec.LookPath("findmnt")
	if err != nil {
		return false, fmt.Errorf("findmnt not found: %w", err)
	}

	out, err := exec.Command(cmdPath, "--json").CombinedOutput()
	if err != nil {
		klog.V(3).Infof("failed to execute command: %+v", cmdPath)
		return false, err
	}

	if len(out) < 1 {
		return false, fmt.Errorf("mount point info is nil")
	}

	mountInfos, err := parseMountInfo([]byte(out))
	if err != nil {
		return false, fmt.Errorf("failed to parse the mount infos: %+v", err)
	}

	mountInfosOfPod := MountPointInfo{}
	for _, mountInfo := range mountInfos {
		if mountInfo.Target == podVolumeTargetPath {
			mountInfosOfPod = mountInfo
			break
		}
	}

	for _, mountInfo := range mountInfosOfPod.ContainerFileSystem {
		if !strings.Contains(mountInfo.Source, volumePath) {
			continue
		}

		_, err = os.Stat(mountInfo.Target)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}

			return false, err
		}

		return true, nil
	}

	return false, nil
}

func (c3 *csi3fs) checkPVCapacityValid(volID string) (bool, error) {
	volumePath := c3.getVolumePath(volID)
	_, fscapacity, _, _, _, _, err := fs.Info(volumePath)
	if err != nil {
		return false, fmt.Errorf("failed to get capacity info: %+v", err)
	}

	volume, err := c3.state.GetVolumeByID(volID)
	if err != nil {
		return false, err
	}
	volumeCapacity := volume.VolSize
	klog.V(3).Infof("volume capacity: %+v fs capacity:%+v", volumeCapacity, fscapacity)
	return fscapacity >= volumeCapacity, nil
}

func getPVStats(volumePath string) (available int64, capacity int64, used int64, inodes int64, inodesFree int64, inodesUsed int64, err error) {
	return fs.Info(volumePath)
}

func (c3 *csi3fs) checkPVUsage(volID string) (bool, error) {
	volumePath := c3.getVolumePath(volID)
	fsavailable, _, _, _, _, _, err := fs.Info(volumePath)
	if err != nil {
		return false, err
	}

	klog.V(3).Infof("fs available: %+v", fsavailable)
	return fsavailable > 0, nil
}

func (c3 *csi3fs) doHealthCheckInControllerSide(volID string) (bool, string) {
	volumePath := c3.getVolumePath(volID)
	klog.V(3).Infof("Volume with ID %s has path %s.", volID, volumePath)
	spExist, err := checkPathExist(volumePath)
	if err != nil {
		return false, err.Error()
	}

	if !spExist {
		return false, "The source path of the volume doesn't exist"
	}

	capValid, err := c3.checkPVCapacityValid(volID)
	if err != nil {
		return false, err.Error()
	}

	if !capValid {
		return false, "The capacity of volume is greater than actual storage"
	}

	available, err := c3.checkPVUsage(volID)
	if err != nil {
		return false, err.Error()
	}

	if !available {
		return false, "The free space of the volume is insufficient"
	}

	return true, ""
}

func (c3 *csi3fs) doHealthCheckInNodeSide(volID string) (bool, string) {
	volumePath := c3.getVolumePath(volID)
	mpExist, err := checkMountPointExist(volumePath)
	if err != nil {
		return false, err.Error()
	}

	if !mpExist {
		return false, "The volume isn't mounted"
	}

	return true, ""
}
