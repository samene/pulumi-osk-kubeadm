package main

import (
	"github.com/pulumi/pulumi-openstack/sdk/v3/go/openstack/compute"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type K8sCluster struct {
	pulumi.ResourceState
}

type Inventory struct {
	ClusterName        string
	User               string
	LoadBalancerIP     string
	LoadBalancer       LoadBalancerDef
	MasterIPs          []string
	WorkerIPs          []string
	Cni                string
	Cri                string
	K8sversion         string
	PrivateRegistry    string
	InsecureRegistries []string
}

type infrastructureConfig struct {
	workerFlavor   string
	masterFlavor   string
	lbFlavor       string
	image          string
	keyPair        *compute.Keypair
	sshUser        string
	floatingIPPool string
	network        string
}

type PortMapping struct {
	Source int `yaml:"source"`
	Target int `yaml:"target"`
}

type LoadBalancerDef struct {
	Create       bool                   `yaml:"create"`
	PortMappings map[string]PortMapping `yaml:"port_mappings"`
}

type Cluster struct {
	KubernetesVersion  string          `yaml:"kubernetes_version"`
	PrivateRegistry    string          `yaml:"private_registry,omitempty"`
	InsecureRegistries []string        `yaml:"insecure_registries,omitempty"`
	LoadBalancer       LoadBalancerDef `yaml:"load_balancer,omitempty"`
	Ntp                struct {
		Primary   string `yaml:"primary"`
		Secondary string `yaml:"secondary"`
	} `yaml:"ntp"`
	ControlPlane struct {
		NodeCount int `yaml:"node_count"`
	} `yaml:"control_plane"`
	Worker struct {
		NodeCount int `yaml:"node_count"`
	} `yaml:"worker"`
	Cni string `yaml:"cni"`
	Cri string `yaml:"cri"`
}

type Topology struct {
	Clusters map[string]Cluster `yaml:"clusters"`
}
