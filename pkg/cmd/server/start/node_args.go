package start

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/kubernetes/pkg/master/ports"
	"k8s.io/kubernetes/pkg/util"
	"k8s.io/kubernetes/pkg/util/sets"

	"github.com/openshift/origin/pkg/cmd/server/admin"
	configapi "github.com/openshift/origin/pkg/cmd/server/api"
	cmdutil "github.com/openshift/origin/pkg/cmd/util"
)

// NodeArgs is a struct that the command stores flag values into.  It holds a partially complete set of parameters for starting a node.
// This object should hold the common set values, but not attempt to handle all cases.  The expected path is to use this object to create
// a fully specified config later on.  If you need something not set here, then create a fully specified config file and pass that as argument
// to starting the master.
type NodeArgs struct {
	// NodeName is the hostname to identify this node with the master.
	NodeName string

	MasterCertDir string
	ConfigDir     util.StringFlag

	AllowDisabledDocker bool
	// VolumeDir is the volume storage directory.
	VolumeDir string

	DefaultKubernetesURL *url.URL
	ClusterDomain        string
	ClusterDNS           net.IP

	// NetworkPluginName is the network plugin to be called for configuring networking for pods.
	NetworkPluginName string

	ListenArg          *ListenArg
	ImageFormatArgs    *ImageFormatArgs
	KubeConnectionArgs *KubeConnectionArgs
}

// BindNodeArgs binds the options to the flags with prefix + default flag names
func BindNodeArgs(args *NodeArgs, flags *pflag.FlagSet, prefix string) {
	flags.StringVar(&args.VolumeDir, prefix+"volume-dir", "openshift.local.volumes", "The volume storage directory.")
	// TODO rename this node-name and recommend uname -n
	flags.StringVar(&args.NodeName, prefix+"hostname", args.NodeName, "The hostname to identify this node with the master.")
	flags.StringVar(&args.NetworkPluginName, prefix+"network-plugin", args.NetworkPluginName, "The network plugin to be called for configuring networking for pods.")

	// autocompletion hints
	cobra.MarkFlagFilename(flags, prefix+"volume-dir")
}

// NewDefaultNodeArgs creates NodeArgs with sub-objects created and default values set.
func NewDefaultNodeArgs() *NodeArgs {
	hostname, err := defaultHostname()
	if err != nil {
		hostname = "localhost"
	}

	var dnsIP net.IP
	if clusterDNS := cmdutil.Env("OPENSHIFT_DNS_ADDR", ""); len(clusterDNS) > 0 {
		dnsIP = net.ParseIP(clusterDNS)
	}

	config := &NodeArgs{
		NodeName: hostname,

		MasterCertDir: "openshift.local.config/master",

		ClusterDomain: cmdutil.Env("OPENSHIFT_DNS_DOMAIN", "cluster.local"),
		ClusterDNS:    dnsIP,

		NetworkPluginName: "",

		ListenArg:          NewDefaultListenArg(),
		ImageFormatArgs:    NewDefaultImageFormatArgs(),
		KubeConnectionArgs: NewDefaultKubeConnectionArgs(),
	}
	config.ConfigDir.Default("openshift.local.config/node")

	return config
}

func (args NodeArgs) Validate() error {
	if err := args.KubeConnectionArgs.Validate(); err != nil {
		return err
	}
	if _, err := args.KubeConnectionArgs.GetKubernetesAddress(args.DefaultKubernetesURL); err != nil {
		return errors.New("--kubeconfig must be set to provide API server connection information")
	}

	return nil
}

// BuildSerializeableNodeConfig takes the NodeArgs (partially complete config) and uses them along with defaulting behavior to create the fully specified
// config object for starting the node
func (args NodeArgs) BuildSerializeableNodeConfig() (*configapi.NodeConfig, error) {
	var dnsIP string
	if len(args.ClusterDNS) > 0 {
		dnsIP = args.ClusterDNS.String()
	}

	config := &configapi.NodeConfig{
		NodeName: args.NodeName,

		ServingInfo: configapi.ServingInfo{
			BindAddress: net.JoinHostPort(args.ListenArg.ListenAddr.Host, strconv.Itoa(ports.KubeletPort)),
		},

		ImageConfig: configapi.ImageConfig{
			Format: args.ImageFormatArgs.ImageTemplate.Format,
			Latest: args.ImageFormatArgs.ImageTemplate.Latest,
		},

		NetworkConfig: configapi.NodeNetworkConfig{
			NetworkPluginName: args.NetworkPluginName,
		},

		VolumeDirectory:     args.VolumeDir,
		AllowDisabledDocker: args.AllowDisabledDocker,

		DNSDomain: args.ClusterDomain,
		DNSIP:     dnsIP,

		MasterKubeConfig: admin.DefaultNodeKubeConfigFile(args.ConfigDir.Value()),

		PodManifestConfig: nil,
	}

	if args.ListenArg.UseTLS() {
		config.ServingInfo.ServerCert = admin.DefaultNodeServingCertInfo(args.ConfigDir.Value())
		config.ServingInfo.ClientCA = admin.DefaultKubeletClientCAFile(args.MasterCertDir)
	}

	internal, err := applyDefaults(config, "v1")
	if err != nil {
		return nil, err
	}

	return internal.(*configapi.NodeConfig), nil
}

// GetServerCertHostnames returns the set of hostnames and IP addresses a serving certificate for node on this host might need to be valid for.
func (args NodeArgs) GetServerCertHostnames() (sets.String, error) {
	allHostnames := sets.NewString(args.NodeName)

	listenIP := net.ParseIP(args.ListenArg.ListenAddr.Host)
	// add the IPs that might be used based on the ListenAddr.
	if listenIP != nil && listenIP.IsUnspecified() {
		allAddresses, _ := cmdutil.AllLocalIP4()
		for _, ip := range allAddresses {
			allHostnames.Insert(ip.String())
		}
	} else {
		allHostnames.Insert(args.ListenArg.ListenAddr.Host)
	}

	certHostnames := sets.String{}
	for hostname := range allHostnames {
		if host, _, err := net.SplitHostPort(hostname); err == nil {
			// add the hostname without the port
			certHostnames.Insert(host)
		} else {
			// add the originally specified hostname
			certHostnames.Insert(hostname)
		}
	}

	return certHostnames, nil
}

// defaultHostname returns the default hostname for this system.
func defaultHostname() (string, error) {
	// Note: We use exec here instead of os.Hostname() because we
	// want the FQDN, and this is the easiest way to get it.
	fqdn, err := exec.Command("uname", "-n").Output()
	if err != nil {
		return "", fmt.Errorf("Couldn't determine hostname: %v", err)
	}
	return strings.ToLower(strings.TrimSpace(string(fqdn))), nil
}
