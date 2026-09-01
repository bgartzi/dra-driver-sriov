package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishvananda/netlink"

	configapi "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/api/virtualfunction/v1alpha1"
	"github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/consts"
)

// VdpaProvider wraps vdpa netlink library calls to allow mocking in unit tests.
type VdpaProvider interface {
	// CreateVDPADevice creates the vdpa device onto an existing VF
	CreateVDPADevice(name, pciAddr string, config *configapi.VdpaConfig) error
	// DeleteVDPADevice deletes the vdpa device
	DeleteVDPADevice(name string) error
	// GetVDPACharDevice finds the char device related to a vdpa device
	GetVDPACharDevice(name string) (string, error)
}

type defaultVdpaProvider struct{}

var _ VdpaProvider = &defaultVdpaProvider{}

func (*defaultVdpaProvider) CreateVDPADevice(
	name,
	pciAddr string,
	config *configapi.VdpaConfig,
) error {
	params := newVDPADevParamsFromConfig(config)
	return netlink.VDPANewDev(name, consts.PciBus, pciAddr, params)
}

func (*defaultVdpaProvider) DeleteVDPADevice(name string) error {
	return netlink.VDPADelDev(name)
}

// Pretty much a govdpa lib rip off
func (*defaultVdpaProvider) GetVDPACharDevice(name string) (string, error) {
	vdpaVhostDevDir := buildSysPathByBusAndDevice(consts.VdpaBus, name, "")
	fd, err := os.Open(vdpaVhostDevDir)
	if err != nil {
		return "", err
	}
	defer fd.Close()

	fileInfos, err := fd.Readdir(-1)
	if err != nil {
		return "", err
	}
	for _, file := range fileInfos {
		if strings.Contains(file.Name(), "vhost-vdpa") &&
			file.IsDir() {
			devicePath := filepath.Join(vdpaVhostDevDir, file.Name())
			info, err := os.Stat(devicePath)
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeDevice == 0 {
				return "", fmt.Errorf("vhost device %s is not a valid device", devicePath)
			}
			return devicePath, nil
		}
	}

	// vhost vdpa devices live in the vdpa device's path
	return "", fmt.Errorf("couldn't find chardevs assigned to vhost-vdpa device on %q", name)
}

func newVDPADevParamsFromConfig(
	config *configapi.VdpaConfig,
) netlink.VDPANewDevParams {
	return netlink.VDPANewDevParams{
		MaxVQP:   config.MaxVQP,
		MTU:      config.MTU,
		Features: config.VirtioFeatureBits,
	}
}

func vdpaDevName(pciAddr string) string {
	return fmt.Sprintf("vdpa:%s", pciAddr)
}
