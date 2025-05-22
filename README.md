# Cilium Kubeadm Environment

This code can be used to create clusters using kubeadm with cilium installed on it on any machine Baremetal or Cloud.

## HOWTO

### Build The Binary

Run the following command:

```go build```

### Initialise Nodes

Below command is used to install and setup nodes for master and worker role.

```
./l4env init --nodes <comma-separated-ips-reachable-from-binary> --user ubuntu --key ~/.ssh/key.pem --kubeadm-version v1.32
```

### Create Cluster

Below command is used to create the Kubernetes cluster with Cilium running on it.

```
./l4env create --masters <comma-separated-masters-reachable-from-binary> --workers <comma-separated-workers-reachable-from-binary>  --user ubuntu --key ~/.ssh/key.pem  --advertise-addr <ip-address-to-advertise(could be loadbalancer of master)> --initial-master-private-addr <first-master-private-address-from-above-list>
```

### Add Worker Nodes

Below command is used to add worker nodes

```
./l4env add-worker --primary <any-master-ip-or-advertised-address-reachable-from-binary> --workers <comma-separated-new-workers-reachable-from-binary> --user ubuntu --key ~/.ssh/key.pem --advertise-addr <cluster-advertised-address>
```