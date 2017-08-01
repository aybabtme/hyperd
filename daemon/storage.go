package daemon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	dockertypes "github.com/docker/engine-api/types"
	"github.com/golang/glog"
	"github.com/hyperhq/hyperd/daemon/daemondb"
	"github.com/hyperhq/hyperd/storage"
	"github.com/hyperhq/hyperd/storage/graphdriver/rawblock"
	"github.com/hyperhq/hyperd/storage/overlay"
	apitypes "github.com/hyperhq/hyperd/types"
	"github.com/hyperhq/hyperd/utils"
	runv "github.com/hyperhq/runv/api"
)

type Storage interface {
	Type() string
	RootPath() string

	Init() error
	CleanUp() error

	PrepareContainer(mountId, sharedDir string, readonly bool) (*runv.VolumeDescription, error)
	CleanupContainer(id, sharedDir string) error
	InjectFile(src io.Reader, containerId, target, baseDir string, perm, uid, gid int) error
	CreateVolume(podId string, spec *apitypes.UserVolume) error
	RemoveVolume(podId string, record []byte) error
}

var StorageDrivers map[string]func(*dockertypes.Info, *daemondb.DaemonDB) (Storage, error) = map[string]func(*dockertypes.Info, *daemondb.DaemonDB) (Storage, error){
	"overlay":  OverlayFsFactory,
	"rawblock": RawBlockFactory,
}

func StorageFactory(sysinfo *dockertypes.Info, db *daemondb.DaemonDB) (Storage, error) {
	if factory, ok := StorageDrivers[sysinfo.Driver]; ok {
		return factory(sysinfo, db)
	}
	return nil, fmt.Errorf("hyperd can not support docker's backing storage: %s", sysinfo.Driver)
}

type OverlayFsStorage struct {
	rootPath string
}

func OverlayFsFactory(_ *dockertypes.Info, _ *daemondb.DaemonDB) (Storage, error) {
	driver := &OverlayFsStorage{
		rootPath: filepath.Join(utils.HYPER_ROOT, "overlay"),
	}
	return driver, nil
}

func (o *OverlayFsStorage) Type() string {
	return "overlay"
}

func (o *OverlayFsStorage) RootPath() string {
	return o.rootPath
}

func (*OverlayFsStorage) Init() error { return nil }

func (*OverlayFsStorage) CleanUp() error { return nil }

func (o *OverlayFsStorage) PrepareContainer(mountId, sharedDir string, readonly bool) (*runv.VolumeDescription, error) {
	_, err := overlay.MountContainerToSharedDir(mountId, o.RootPath(), sharedDir, "", readonly)
	if err != nil {
		glog.Error("got error when mount container to share dir ", err.Error())
		return nil, err
	}

	containerPath := "/" + mountId
	vol := &runv.VolumeDescription{
		Name:     containerPath,
		Source:   containerPath,
		Fstype:   "dir",
		Format:   "vfs",
		ReadOnly: readonly,
	}

	return vol, nil
}

func (o *OverlayFsStorage) CleanupContainer(id, sharedDir string) error {
	return syscall.Unmount(filepath.Join(sharedDir, id, "rootfs"), 0)
}

func (o *OverlayFsStorage) InjectFile(src io.Reader, mountId, target, baseDir string, perm, uid, gid int) error {
	_, err := overlay.MountContainerToSharedDir(mountId, o.RootPath(), baseDir, "", false)
	if err != nil {
		glog.Error("got error when mount container to share dir ", err.Error())
		return err
	}
	defer syscall.Unmount(filepath.Join(baseDir, mountId, "rootfs"), 0)

	return storage.FsInjectFile(src, mountId, target, baseDir, perm, uid, gid)
}

func (o *OverlayFsStorage) CreateVolume(podId string, spec *apitypes.UserVolume) error {
	volName, err := storage.CreateVFSVolume(podId, spec.Name)
	if err != nil {
		return err
	}
	spec.Source = volName
	spec.Format = "vfs"
	spec.Fstype = "dir"
	return nil
}

func (o *OverlayFsStorage) RemoveVolume(podId string, record []byte) error {
	return nil
}

type RawBlockStorage struct {
	rootPath string
}

func RawBlockFactory(_ *dockertypes.Info, _ *daemondb.DaemonDB) (Storage, error) {
	driver := &RawBlockStorage{
		rootPath: filepath.Join(utils.HYPER_ROOT, "rawblock"),
	}
	return driver, nil
}

func (s *RawBlockStorage) Type() string {
	return "rawblock"
}

func (s *RawBlockStorage) RootPath() string {
	return s.rootPath
}

func (s *RawBlockStorage) Init() error {
	if err := os.MkdirAll(filepath.Join(s.RootPath(), "volumes"), 0700); err != nil {
		return err
	}
	return nil
}

func (*RawBlockStorage) CleanUp() error { return nil }

func (s *RawBlockStorage) PrepareContainer(containerId, sharedDir string, readonly bool) (*runv.VolumeDescription, error) {
	devFullName := filepath.Join(s.RootPath(), "blocks", containerId)

	vol := &runv.VolumeDescription{
		Name:     devFullName,
		Source:   devFullName,
		Fstype:   "xfs",
		Format:   "raw",
		ReadOnly: readonly,
	}

	return vol, nil
}

func (s *RawBlockStorage) CleanupContainer(id, sharedDir string) error {
	return nil
}

func (s *RawBlockStorage) InjectFile(src io.Reader, mountId, target, baseDir string, perm, uid, gid int) error {
	if err := rawblock.GetImage(filepath.Join(s.RootPath(), "blocks"), baseDir, mountId, "xfs", "", uid, gid); err != nil {
		return err
	}
	defer rawblock.PutImage(baseDir, mountId)
	return storage.FsInjectFile(src, mountId, target, baseDir, perm, uid, gid)
}

func (s *RawBlockStorage) CreateVolume(podId string, spec *apitypes.UserVolume) error {
	block := filepath.Join(s.RootPath(), "volumes", fmt.Sprintf("%s-%s", podId, spec.Name))
	if err := rawblock.CreateBlock(block, "xfs", "", uint64(storage.DEFAULT_DM_VOL_SIZE)); err != nil {
		return err
	}
	spec.Source = block
	spec.Fstype = "xfs"
	spec.Format = "raw"
	return nil
}

func (s *RawBlockStorage) RemoveVolume(podId string, record []byte) error {
	return nil
}
