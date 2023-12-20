// @author: sameer.mene

package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi-openstack/sdk/v3/go/openstack/compute"
	"github.com/pulumi/pulumi-openstack/sdk/v3/go/openstack/networking"
	"gopkg.in/yaml.v3"

	"text/template"

	_ "embed"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/rs/zerolog/log"
)

//go:embed inventory.tmpl
var inventoryTmpl []byte

//go:embed variables.tmpl
var variablesTmpl []byte

func readTopology(filename string) *Topology {
	topology := &Topology{}
	topo, err := os.ReadFile(filename)
	if err != nil {
		d, _ := os.Getwd()
		log.Fatal().Err(err).Msgf("Cannot open topology file %s. Full path of file is %s", filename, d)
	}
	err = yaml.Unmarshal(topo, &topology)
	if err != nil {
		log.Fatal().Err(err).Msgf("Cannot unmarshal topology file %s, is it in correct format?", filename)
	}
	return topology
}

func NewK8sCluster(ctx *pulumi.Context, name string, opts ...pulumi.ResourceOption) (*K8sCluster, error) {
	k8sCluster := &K8sCluster{}
	err := ctx.RegisterComponentResource("pkg:k8s:K8sCluster", name, k8sCluster, opts...)
	if err != nil {
		return nil, err
	}
	return k8sCluster, nil
}

func main() {
	pulumi.Run(start)
}

func readConfig(ctx *pulumi.Context) (*infrastructureConfig, *Topology) {
	conf := config.New(ctx, "")
	infraCfg := &infrastructureConfig{}
	infraCfg.workerFlavor = conf.Require("workerFlavor")
	infraCfg.masterFlavor = conf.Require("masterFlavor")
	infraCfg.lbFlavor = conf.Require("loadbalancerFlavor")
	infraCfg.image = conf.Require("image")
	infraCfg.sshUser = conf.Require("sshUser")
	infraCfg.network = conf.Require("network")
	infraCfg.floatingIPPool = conf.Require("floatingIpPool")
	topologyFile := conf.Require("topologyFile")
	topology := readTopology(topologyFile)
	return infraCfg, topology
}

func generateKeys(ctx *pulumi.Context) (*compute.Keypair, error) {
	keyPair, err := compute.NewKeypair(ctx, "kubeadm-keypair", nil)
	if err != nil {
		return nil, err
	}
	_, err = local.NewCommand(ctx, "gen-privatekey", &local.CommandArgs{
		Create: keyPair.PrivateKey.ApplyT(func(privateKey string) string {
			er := os.WriteFile("./id_rsa", []byte(privateKey), 0600)
			if er != nil {
				return "false"
			}
			return "echo \"./id_rsa created\""
		}).(pulumi.StringInput),
		Delete: pulumi.String("rm -rf ./id_rsa"),
	})
	if err != nil {
		return nil, err
	}
	return keyPair, nil
}

func start(ctx *pulumi.Context) error {
	infraCfg, topology := readConfig(ctx)
	clusterConfigs := make([]interface{}, 0)

	keyPair, err := generateKeys(ctx)
	if err != nil {
		return err
	}
	infraCfg.keyPair = keyPair

	for cName, cluster := range topology.Clusters {
		var lb, mst, wrk pulumi.Output
		clusterName := cName
		clusterIterator := cluster
		pulumik8sCluster, err := NewK8sCluster(ctx, clusterName)
		if err != nil {
			return err
		}

		lbIPs := make([]string, 0)
		if cluster.ControlPlane.NodeCount > 1 || cluster.LoadBalancer.Create {
			ip, err := createInstance(ctx, infraCfg.lbFlavor, infraCfg, fmt.Sprintf("%s-loadbal", clusterName), true, pulumik8sCluster)
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to create loadbalancer instance")
			}
			lb = ip.ApplyT(func(ipAddress string) []string {
				lbIPs = append(lbIPs, ipAddress)
				return []string{ipAddress}
			}).(pulumi.StringArrayOutput)
		}

		// control plane
		ips := make([]interface{}, 0)
		masterIPs := make([]string, 0)
		for instanceIndex := 0; instanceIndex < cluster.ControlPlane.NodeCount; instanceIndex++ {
			var ip pulumi.Output
			if cluster.Worker.NodeCount == 0 {
				ip, err = createInstance(ctx, infraCfg.workerFlavor, infraCfg, fmt.Sprintf("%s-master-worker", clusterName), true, pulumik8sCluster)
			} else {
				ip, err = createInstance(ctx, infraCfg.masterFlavor, infraCfg, fmt.Sprintf("%s-master-%d", clusterName, instanceIndex), true, pulumik8sCluster)
			}
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to create control plane instance(s)")
			}
			ips = append(ips, ip.(pulumi.StringOutput))
		}
		mst = pulumi.All(ips...).ApplyT(func(ips []interface{}) []string {
			var retIps []string = make([]string, 0)
			for _, ip := range ips {
				masterIPs = append(masterIPs, fmt.Sprintf("%s", ip))
				retIps = append(retIps, ip.(string))
			}
			return retIps
		}).(pulumi.StringArrayOutput)

		// worker
		ips = make([]interface{}, 0)
		workerIPs := make([]string, 0)
		for instanceIndex := 0; instanceIndex < cluster.Worker.NodeCount; instanceIndex++ {
			var ip pulumi.Output
			ip, err = createInstance(ctx, infraCfg.workerFlavor, infraCfg, fmt.Sprintf("%s-worker-%d", clusterName, instanceIndex), true, pulumik8sCluster)
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to create worker instance(s)")
			}
			ips = append(ips, ip.(pulumi.StringOutput))
		}
		if len(ips) != 0 {
			wrk = pulumi.All(ips...).ApplyT(func(ips []interface{}) []string {
				var retIps []string = make([]string, 0)
				for _, ip := range ips {
					workerIPs = append(workerIPs, fmt.Sprintf("%s", ip))
					retIps = append(retIps, ip.(string))
				}
				return retIps
			}).(pulumi.StringArrayOutput)
		}

		var toWait []interface{} = make([]interface{}, 0)
		if lb != nil {
			toWait = append(toWait, lb)
		}
		toWait = append(toWait, mst)
		if wrk != nil {
			toWait = append(toWait, wrk)
		}

		ctx.RegisterResourceOutputs(pulumik8sCluster, pulumi.Map{
			"clusterName": pulumi.String(clusterName),
		})

		_, err = local.NewCommand(ctx, fmt.Sprintf("gen-inventory-%s", clusterName), &local.CommandArgs{
			Create: pulumi.All(toWait...).ApplyT(func(notUsed []interface{}) (string, error) {
				inventory := Inventory{User: infraCfg.sshUser,
					ClusterName:        clusterName,
					Cni:                clusterIterator.Cni,
					Cri:                clusterIterator.Cri,
					PrivateRegistry:    clusterIterator.PrivateRegistry,
					InsecureRegistries: clusterIterator.InsecureRegistries,
					K8sversion:         clusterIterator.KubernetesVersion,
					LoadBalancer:       clusterIterator.LoadBalancer}
				for _, ip := range lbIPs {
					inventory.LoadBalancerIP = ip
				}
				inventory.MasterIPs = append(inventory.MasterIPs, masterIPs...)
				inventory.WorkerIPs = append(inventory.WorkerIPs, workerIPs...)
				genInventoryFile(inventory)
				return fmt.Sprintf("mv /tmp/inventory-%s.ini ./inventory-%s.ini && mv /tmp/variables-%s.yaml ./variables-%s.yaml && echo \"done\"", clusterName, clusterName, clusterName, clusterName), nil
			}).(pulumi.StringOutput),
			AssetPaths: pulumi.ToStringArray([]string{"inventory-" + clusterName + ".ini"}),
			Delete:     pulumi.StringPtr("rm -rf inventory-" + clusterName + ".ini & rm -rf variables-" + clusterName + ".yaml"),
		}, pulumi.Parent(pulumik8sCluster))
		if err != nil {
			return err
		}
		k8sAnsible, err := local.NewCommand(ctx, fmt.Sprintf("ansible-installk8s-%s", clusterName), &local.CommandArgs{
			Create: pulumik8sCluster.URN().ApplyT(func(urn string) (string, error) {
				return fmt.Sprintf("ansible-playbook -i ./inventory-%s.ini -e \"@variables-%s.yaml\" ./install.yaml", clusterName, clusterName), nil
			}).(pulumi.StringOutput),
			Delete: pulumi.StringPtr("rm -rf cluster-" + clusterName + ".kubeconfig"),
			AssetPaths: pulumi.ToStringArray([]string{"cluster-" + clusterName + ".kubeconfig",
				"inventory-" + clusterName + ".ini"}),
		})
		if err != nil {
			return err
		}
		kubeConfig := k8sAnsible.AssetPaths.ApplyT(func(kubeconfigPaths []string) (map[string]interface{}, error) {
			ret := make(map[string]interface{}, 0)
			cConfig := make(map[string]interface{})
			endPointConfig := make(map[string]interface{})
			kc, err := os.ReadFile(kubeconfigPaths[0])
			if err != nil {
				return nil, nil
			}
			inv, err := os.ReadFile(kubeconfigPaths[1])
			if err != nil {
				return nil, nil
			}
			privateKey, err := os.ReadFile("./id_rsa")
			if err != nil {
				return nil, nil
			}
			if len(lbIPs) > 0 {
				endPointConfig["app"] = lbIPs[0]
				endPointConfig["cluster-api"] = lbIPs[0]
				endPointConfig["type"] = "LoadBalancer"
			} else {
				if len(workerIPs) > 0 {
					endPointConfig["app"] = workerIPs[0]
				} else {
					endPointConfig["app"] = masterIPs[0]
				}
				endPointConfig["cluster-api"] = masterIPs[0]
				endPointConfig["type"] = "NodePort"
			}
			cConfig["endpoints"] = endPointConfig
			cConfig["kubeconfig"] = string(kc)
			cConfig["inventory"] = string(inv)
			cConfig["privateKey"] = string(privateKey)

			ret[clusterName] = cConfig
			return ret, nil
		}).(pulumi.MapOutput)
		clusterConfigs = append(clusterConfigs, kubeConfig)
	}
	output := pulumi.All(clusterConfigs...).ApplyT(func(k []interface{}) []map[string]interface{} {
		clusters := make([]map[string]interface{}, 0)
		for _, kconfig := range k {
			for cname, kc := range kconfig.(map[string]interface{}) {
				entry := make(map[string]interface{}, 0)
				entry[cname] = kc
				clusters = append(clusters, entry)
			}
		}
		return clusters
	}).(pulumi.MapArrayOutput)
	ctx.Export("clusters", pulumi.ToSecret(output))

	return nil
}

func createInstance(ctx *pulumi.Context, flavor string,
	infraCfg *infrastructureConfig,
	instanceName string,
	createFloatingIp bool,
	pulumik8sCluster *K8sCluster) (pulumi.Output, error) {

	instance, err := compute.NewInstance(ctx, instanceName, &compute.InstanceArgs{
		Name:       pulumi.String(instanceName),
		FlavorName: pulumi.String(flavor),
		ImageName:  pulumi.String(infraCfg.image),
		KeyPair: infraCfg.keyPair.Name.ApplyT(func(name string) string {
			return name
		}).(pulumi.StringOutput),
		Networks: compute.InstanceNetworkArray{&compute.InstanceNetworkArgs{Name: pulumi.StringPtr(infraCfg.network)}},
	}, pulumi.Parent(pulumik8sCluster))
	if err != nil {
		return nil, err
	}
	if createFloatingIp {
		fip := fmt.Sprintf("fip-%s", instanceName)
		fip1FloatingIp, err := networking.NewFloatingIp(ctx, fip, &networking.FloatingIpArgs{
			Pool: pulumi.String(infraCfg.floatingIPPool),
		}, pulumi.Parent(pulumik8sCluster))
		if err != nil {
			return nil, err
		}
		_, err = compute.NewFloatingIpAssociate(ctx, fip, &compute.FloatingIpAssociateArgs{
			FloatingIp: fip1FloatingIp.Address,
			InstanceId: instance.ID(),
		}, pulumi.Parent(pulumik8sCluster))
		if err != nil {
			return nil, err
		}
		return fip1FloatingIp.Address, nil
	} else {
		return instance.AccessIpV4, nil
	}
}

func genInventoryFile(clusterInventory Inventory) {
	renderedTemplate, parseErr := template.New("invtpl").Parse(string(inventoryTmpl))
	if parseErr != nil {
		log.Fatal().Err(parseErr).Msg("Error parsing template file")
	}
	var buff bytes.Buffer
	if err := renderedTemplate.Execute(&buff, clusterInventory); err != nil {
		log.Fatal().Err(err).Msg("Failed to render inventory template ")
	}

	outFileLoc := fmt.Sprintf("/tmp/inventory-%s.ini", clusterInventory.ClusterName)

	if err := os.WriteFile(outFileLoc, buff.Bytes(), 0655); err != nil {
		log.Fatal().Msg("Failed to write inventory files ")
	}

	buff.Reset()
	renderedTemplate, parseErr = template.New("invtpl").Parse(string(variablesTmpl))
	if parseErr != nil {
		log.Fatal().Err(parseErr).Msg("Error parsing template file")
	}
	if err := renderedTemplate.Execute(&buff, clusterInventory); err != nil {
		log.Fatal().Err(err).Msg("Failed to render inventory template ")
	}

	outFileLoc = fmt.Sprintf("/tmp/variables-%s.yaml", clusterInventory.ClusterName)

	if err := os.WriteFile(outFileLoc, buff.Bytes(), 0655); err != nil {
		log.Fatal().Msg("Failed to write inventory files ")
	}

}
