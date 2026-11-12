package engine

import (
	"net/netip"
	"strconv"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// Labels of each object that the CLI makes.
const (
	LabelManagedBy = "dev.mai.managed-by"
	ManagedByValue = "mai-cli"
	LabelInstance  = "dev.mai.instance"
)

// ContainerPort is the HTTP port of the server in the container.
const ContainerPort = 8080

// StopTimeout is the time in seconds that a stop waits before a kill.
const StopTimeout = 60

// Spec is the input of the container definition.
type Spec struct {
	Instance string
	Port     int
	Image    string
	DataDir  string // a host folder for /data; empty means the volume <instance>3-data
}

// DataVolume is the name of the data volume of an instance.
func DataVolume(instance string) string { return instance + "3-data" }

// SpoolVolume is the name of the spool volume of an instance.
func SpoolVolume(instance string) string { return instance + "3-spool" }

// Labels returns the labels of each object of the instance.
func Labels(instance string) map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue, LabelInstance: instance}
}

// Capabilities that the container adds after it drops all.
var Capabilities = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID", "KILL"}

// TmpfsPaths are the tmpfs folders of the container. /var/lib/postgresql stops the
// anonymous volume that the base image declares.
var TmpfsPaths = []string{"/run", "/tmp", "/etc/postfix", "/etc/dovecot", "/var/lib/postgresql"}

// Volumes returns the names of the volumes that the instance uses. With a host data folder,
// only the spool is a volume.
func (s Spec) Volumes() []string {
	if s.DataDir != "" {
		return []string{SpoolVolume(s.Instance)}
	}
	return []string{DataVolume(s.Instance), SpoolVolume(s.Instance)}
}

// CreateOptions returns the container definition of the contract.
func (s Spec) CreateOptions() client.ContainerCreateOptions {
	port := network.MustParsePort(strconv.Itoa(ContainerPort) + "/tcp")
	stop := StopTimeout
	data := mount.Mount{Type: mount.TypeVolume, Source: DataVolume(s.Instance), Target: "/data"}
	if s.DataDir != "" {
		data = mount.Mount{Type: mount.TypeBind, Source: s.DataDir, Target: "/data"}
	}
	tmpfs := map[string]string{}
	for _, p := range TmpfsPaths {
		tmpfs[p] = ""
	}
	return client.ContainerCreateOptions{
		Name: s.Instance,
		Config: &container.Config{
			Image:        s.Image,
			Labels:       Labels(s.Instance),
			Env:          []string{"MAI_HOSTS=127.0.0.1:" + strconv.Itoa(s.Port)},
			ExposedPorts: network.PortSet{port: struct{}{}},
			StopTimeout:  &stop,
		},
		HostConfig: &container.HostConfig{
			PortBindings: network.PortMap{port: {{
				HostIP:   netip.MustParseAddr("127.0.0.1"),
				HostPort: strconv.Itoa(s.Port),
			}}},
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			CapAdd:         append([]string(nil), Capabilities...),
			Tmpfs:          tmpfs,
			Mounts: []mount.Mount{
				data,
				{Type: mount.TypeVolume, Source: SpoolVolume(s.Instance), Target: "/var/lib/mai/spool"},
			},
		},
	}
}
