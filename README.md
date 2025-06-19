# MITM Env

This code can be used to create clusters using kubeadm with cilium installed on it on any machine Baremetal or Cloud.

## HOWTO

Before running the below-mentioned make command, please edit your stage.tfvars to provide your environment details:
Your SSH key to be added to the FRR instance
VPC ID for your network to set up instances
Subnet ID for your subnet to set up instances

Currently, Terraform doesn’t set up the required Network infrastructure in AWS. This is in the pipeline for an easier development setup.

To set up infrastructure for dog-fooding, run:
```
make dev.newawsenv
```
This sets up the nodes for two components:
FRR router:
The router simulates the on-premises router in a dog-fooding environment.
Node instances for the MITM instance
4 Nodes are set up in the VPC for building the Kubernetes cluster.

Running the above command should give an output similar to:

```
Apply complete! Resources: 9 added, 0 changed, 0 destroyed.

Outputs:

asg_instance_private_ips = [
  "172.31.21.82",
  "172.31.22.184",
  "172.31.25.244",
  "172.31.28.49",
]
asg_name = "asg-group"
frr_instance_private_ip = "172.31.27.229"
frr_instance_public_ip = "107.21.167.110"
ssh_private_key_path = "~/.ssh/k8s-tanuj.pem"
```
You can use the FRR Public IP to SSH into the router and follow the instructions in the Dogfood environment below to continue with the MITM installation.

The ASG Instance Private IPs are your private MITM instances node IPs and can be used as input to the l4env CLI to set up the MITM instances.

### Known Limitation:
Terraform doesn’t setup the required VPC/Subet/AMI images required and need them to be preconfigured.

Action Item: Extend Terraform module to create the network infrastructure

### MITM Instance setup
To set up an MITM instance, you need access to the nodes for the Kubernetes cluster setup.

#### Steps for MITM instance setup:
Install the required dependencies on Kubernetes nodes
Create a cluster using kube-adm and the provided nodes
Create the configuration for the MITM instance:
Hosts to be monitored
HCP Vault configuration for certificate generation
OpsInterface URL for sending processed data
	An example of the configuration can be found here.
Deploy the MITM instance along with the generated configuration in the Kubernetes cluster. The MITM configuration can be found here

To simplify the above process, we have a CLI utility that encapsulates the steps above: https://github.com/tetrateio/mitm-env

The utility CLI Inputs:
Node IPs to connect with the nodes
SSH key to SSH into the nodes
MITM configuration secret as part of the configuration file. The secret needs to have the config as base64 encoded in the config file. This is known limitation.

It does the following steps on nodes:
Sets up the Kubernetes cluster
Installs Cilium on the Kubernetes cluster and configures the BGP routes for the virtual address.
Installs the MITM instance with the configuration secret provided as part of the configuration file.
On-premise setup instructions
Run the init command to set up the nodes, ie, installing the necessary dependencies on the nodes

```
./l4env_amd64 init --nodes <MITM INSTANCE NODE IPs> --user ubuntu --key <SSH KEY TO NODES> --kubeadm-version v1.32
```
The above command accesses the nodes via SSH to install the required dependencies
MITM INSTANCE NODE IPs: IPs to SSH into different nodes, accessible from the machine where this command is run from
SSH KEY TO NODES: SSH key to log in to the nodes


Run the Create command to set up the Kubernetes cluster, Cilium and MITM instance on the nodes

```
./l4env_amd64 create --masters <MASTER NODE IPs> --workers <WORKER NODE IPs> --user ubuntu --key <SSH KEY TO NODES> --values-file <CONFIG FILE>
```
The above command accesses the nodes via SSH to set up a Kubernetes cluster, Cilium and deploy the MITM daemonset on the cluster. 
MASTER NODE IPs: A Subset of Node IPs to be marked as Master in the Kubernetes cluster.
WORKER NODE IPs: A subset of Node IPs to be marked as Workers in the Kubernetes cluster.
SSH KEY TO NODES: SSH Key to log in to the nodes
CONFIG FILE: Config file for the binary. This config file allows configuration of different components CLI creates on the instance nodes.
