# pulumi-osk-kubeadm

A simple [Pulumi](https://www.pulumi.com/) project in Go to create OpenStack instances and install a kubernetes cluster on them using kubeadm

## Pre-requisites
- Go installed (min 1.18) - [How-to](https://go.dev/doc/install)
- Pulumi installed (latest version recommended) - [How-to](https://www.pulumi.com/docs/install/)
- Ansible installed (latest version recommended)
- An OpenStack cluster with API access privileges
- Supported images - `Ubuntu 22.04`, `RedHat Enterprise Linux 8`, `CentOS 7`
- Enough Floating IP's
- Unrestricted internet access to all spawned compute instances

## How to Run

### Clone Repository

```
git clone git@gitlab.com:samene/pulumi-osk-kubeadm.git
cd pulumi-osk-kubeadm
``````

### Initialize stack (only once)

```
pulumi stack init dev
```
> **_NOTE:_**  Pulumi config data for this project will be stored in folder `./.pulumi` by default.

### Configure Openstack Settings

Set configuration for compute and networking

```
pulumi config set loadbalancerFlavor gp.tiny       # replace with your desired flavor for load balancer
pulumi config set masterFlavor gp.small            # replace with your desired flavor for master nodes
pulumi config set workerFlavor gp.xmedium          # replace with your desired flavor for worker nodes
pulumi config set sshUser ubuntu                   # replace with the name of the ssh user for this OS
pulumi config set image ubuntu-22.04               # replace with your image name
pulumi config set floatingIpPool ext_net           # replace with network name that assigns floating IP's
pulumi config set network development_net          # replace with network name for compute instance
```

Set configuration for authentication to OpenStack server. You may set openstack configuration sourcing a `env` file as mentioned [here](https://docs.openstack.org/newton/user-guide/common/cli-set-environment-variables-using-openstack-rc.html) or if you have the details handy, set the configuration as below. 

```
pulumi config set openstack:authUrl http://10.94.0.106:5000/v3   # replace with auth URL
pulumi config set openstack:tenantName development               # replace with tenant name
pulumi config set openstack:userName myuser                      # replace with your username
pulumi config set openstack:insecure true                        # if untrusted TLS certificate
pulumi config set openstack:password mypassword --secret         # replace with your password
pulumi config set openstack:insecure true                        # if untrusted TLS certificate

# following may be optional
pulumi config set openstack:tenantId 39ddcca9f48a484c8a3c5bdb9be085a5       # get this information from your OSK
pulumi config set openstack:userDomainName admin_domain                     # get this information from your OSK
pulumi config set openstack:region RegionOne                                # get this information from your OSK
```

Set the path of the topology file (relative to current folder, or absolute path)

```
pulumi config set topologyFile topology.yaml
```

### Configure topology

Create a file called `topology.yaml` with following format

```
clusters:
  central:
    kubernetes_version: 1.23 # the highest patch version will be installed     
    private_registry: my-docker-registry.com:5000/subpath
    insecure_registries:     # list of docker registries to add to insecure registries
    - "10.90.84.113:5000"    
    load_balancer:
      create: true           # create a load balancer node
      port_mappings:         # target port mappings
        https:
          source: 443
          target: 31390
        http:
          source: 80
          target: 31394
    ntp:
      primary: 10.17.0.10
      secondary: 10.17.0.11
    control_plane:
      node_count: 3   # 1 or 3 (if 3, one Load Balancer will be created)
    worker:
      node_count: 4   # if 0, control plane will be untainted to schedule workloads
    cni: flannel      # flannel or cilium
  edge-1:
    kubernetes_version: 1.23
    private_registry: my-docker-registry.com:5000/subpath
    insecure_registries: []
    load_balancer:
      create: false
    ntp:
      primary: 10.17.0.10
      secondary: 10.17.0.11
    control_plane:
      node_count: 1
    worker:
      node_count: 0
    cni: flannel
```

### Run

```
pulumi up
```

The end result will be kubeconfig file(s) in your current directory for the newly created clusters.
