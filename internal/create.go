package internal

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	yaml "gopkg.in/yaml.v3"
)

var (
	masters                  string
	workers                  string
	sshPort                  int
	sshUser                  string
	sshKeyPath               string
	sshPassword              string
	advertiseAddr            string
	initialMasterPrivateAddr string
	podCIDR                  string
	enableKubeProxy          bool
)

// ClusterCommand returns the Cobra command for creating a Kubernetes cluster
func ClusterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Initialize a multi-master Kubernetes cluster and join optional workers over SSH",
		Run:   run,
	}

	// SSH flags
	cmd.Flags().StringVar(&masters, "masters", "", "Comma-separated list of master node IPs (first is initial)")
	cmd.Flags().StringVar(&workers, "workers", "", "Comma-separated list of worker node IPs (optional)")
	cmd.Flags().IntVar(&sshPort, "port", 22, "SSH port")
	cmd.Flags().StringVar(&sshUser, "user", "root", "SSH user")
	cmd.Flags().StringVar(&sshKeyPath, "key", "", "Path to private key file")
	cmd.Flags().StringVar(&sshPassword, "password", "", "SSH password (optional)")
	cmd.Flags().BoolVar(&enableKubeProxy, "enable-kube-proxy", false, "Enable kube-proxy addon (default: false)")

	// kubeadm flags
	cmd.Flags().StringVar(&advertiseAddr, "advertise-addr", "", "API server advertise address VIP")
	cmd.Flags().StringVar(&initialMasterPrivateAddr, "initial-master-private-addr", "", "initial master private address if masters are public")
	cmd.Flags().StringVar(&podCIDR, "pod-cidr", "192.168.0.0/16", "Pod network CIDR")

	// Required flags
	cmd.MarkFlagRequired("masters")
	cmd.MarkFlagRequired("advertise-addr")
	cmd.MarkFlagRequired("kube-version")

	return cmd
}

func run(cmd *cobra.Command, args []string) {
	// Validate SSH auth
	if sshKeyPath == "" && sshPassword == "" {
		log.Fatalf("Either --key or --password must be provided for SSH authentication")
	}

	if initialMasterPrivateAddr == "" {
		initialMasterPrivateAddr = advertiseAddr
	}
	masterList := filterEmpty(strings.Split(masters, ","))
	initialMaster := masterList[0]

	workerList := filterEmpty(strings.Split(workers, ","))

	// Prepare SSH auth methods
	authMethods := []ssh.AuthMethod{}
	if sshKeyPath != "" {
		buf, err := os.ReadFile(sshKeyPath)
		if err != nil {
			log.Fatalf("Failed to read private key file: %v", err)
		}
		signer, err := ssh.ParsePrivateKey(buf)
		if err != nil {
			log.Fatalf("Failed to parse private key: %v", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if sshPassword != "" {
		authMethods = append(authMethods, ssh.Password(sshPassword))
	}

	skipPhases := ""
	if !enableKubeProxy {
		skipPhases = "--skip-phases=addon/kube-proxy"
	}
	// 1. Initialize the first master
	initCmd := fmt.Sprintf(`
if [ ! -f /etc/kubernetes/admin.conf ]; then
  sudo kubeadm init --control-plane-endpoint "%s:6443" \
    --apiserver-advertise-address %s \
    --pod-network-cidr %s %s
fi
`, initialMasterPrivateAddr, initialMasterPrivateAddr, podCIDR, skipPhases)
	fmt.Printf("Initializing primary master: %s\n", initialMaster)
	runCmd(initialMaster, initCmd)
	fmt.Printf("Primary master %s initialized.\n", initialMaster)

	// 2. Retrieve join credentials
	fmt.Println("Retrieving join credentials...")
	fmt.Println("Copying kubeconfig to /root/.kube/config...")
	runCmd(initialMaster, "sudo mkdir -p /root/.kube")
	runCmd(initialMaster, "sudo cp /etc/kubernetes/admin.conf /root/.kube/config")
	runCmd(initialMaster, "sudo chown root:root /root/.kube/config")

	kubeProxyReplacement := "true"
	if !enableKubeProxy {
		kubeProxyReplacement = "false"
	}
	// 3. Install CNI plugin (Cilium in this case)
	cniCmd := strings.Join([]string{
		"sudo helm repo add cilium https://helm.cilium.io/",
		"sudo helm repo update",
		"sudo helm upgrade --install cilium cilium/cilium --version 1.17.3 " +
			"--namespace kube-system " +
			"--set egressGateway.enabled=true " +
			"--set bgpControlPlane.enabled=true " +
			"--set bgp.enabled=true " +
			"--set routingMode=tunnel " +
			"--set bpf.masquerade=true " +
			"--set tunnelProtocol=geneve " +
			fmt.Sprintf("--set kubeProxyReplacement=%s ", kubeProxyReplacement) +
			"--set loadBalancer.mode=dsr " +
			"--set loadBalancer.algorithm=maglev " +
			"--set loadBalancer.dsrDispatch=geneve " +
			"--set ipam.operator.clusterPoolIPv4PodCIDRList=10.42.0.0/16 " +
			"--set ipam.operator.clusterPoolIPv4MaskSize=26 " +
			fmt.Sprintf("--set k8sServiceHost=%s ", initialMasterPrivateAddr) +
			"--set k8sServicePort=6443",
	}, " && ")

	fmt.Println("Installing CNI plugin (Cilium)...")
	runCmd(initialMaster, cniCmd)

	// check if cilium is up.
	timeout := 5 * time.Minute
	start := time.Now()
	for {
		ciliumStatus := runCmd(initialMaster, "sudo kubectl get pods -n kube-system -l k8s-app=cilium -o jsonpath='{.items[0].status.phase}'")
		if ciliumStatus == "Running" {
			break
		}
		if time.Since(start) > timeout {
			log.Fatalf("Timed out after %v waiting for Cilium to be up, last status: %q", timeout, ciliumStatus)
		}
		log.Printf("Waiting for Cilium to be up, current status: %s", ciliumStatus)
		time.Sleep(5 * time.Second)
	}

	// update kubeadm config to use advertiseAddr
	fmt.Println("Updating kubeadm config to use advertiseAddr...")
	clusterConfig := runCmd(initialMaster, "sudo kubectl -n kube-system get configmap kubeadm-config -o jsonpath='{.data.ClusterConfiguration}'")
	fmt.Println("Cluster config: \n", clusterConfig)
	// Parse YAML as map for flexibility
	var cc map[string]interface{}
	if err := yaml.Unmarshal([]byte(clusterConfig), &cc); err != nil {
		log.Fatalf("unmarshal error: %w", err)
	}
	cc["controlPlaneEndpoint"] = fmt.Sprintf("%s:6443", advertiseAddr)
	updated, err := yaml.Marshal(&cc)
	if err != nil {
		log.Fatalf("marshal error: %w", err)
	}

	// Write to temp file on the remote host
	tmpFile := "/tmp/cc.yaml"
	// Copy file: use scp, or echo via SSH
	// Example:
	runCmd(initialMaster, fmt.Sprintf("echo \"%s\" > %s", string(updated), tmpFile))

	// Now update the ConfigMap with kubectl
	patchCmd := fmt.Sprintf("kubectl -n kube-system create configmap kubeadm-config --from-file=ClusterConfiguration=%s --dry-run=client -o yaml | kubectl -n kube-system replace -f -", tmpFile)
	runCmd(initialMaster, patchCmd)
	// 3. Retrieve credentials
	timeout = 5 * time.Minute
	start = time.Now()
	var joinOutput string
	for {
		joinOutput = runCmd(initialMaster, "sudo kubeadm token create --print-join-command")
		if strings.Contains(joinOutput, "--token") && strings.Contains(joinOutput, "--discovery-token-ca-cert-hash") {
			break
		}
		if time.Since(start) > timeout {
			log.Fatalf("Timed out after %v waiting for join command, last output: %q", timeout, joinOutput)
		}
		log.Printf("Waiting for valid join command, retrying in 5s... last output: %q", joinOutput)
		time.Sleep(5 * time.Second)
	}
	// Parse token and CA hash
	split := strings.Fields(joinOutput)
	var token, caHash string
	for i, s := range split {
		if s == "--token" && i+1 < len(split) {
			token = split[i+1]
		}
		if s == "--discovery-token-ca-cert-hash" && i+1 < len(split) {
			caHash = split[i+1]
		}
	}
	if token == "" || caHash == "" {
		log.Fatalf("failed to parse token or CA hash after retries: %q", joinOutput)
	}
	fmt.Println("token:", token)
	fmt.Println("caHash:", caHash)

	// 4. Retrieve certificate key for control-plane join with timeout-based retry
	start = time.Now()
	var uploadOutput, certKey string
	for {
		uploadOutput = runCmd(initialMaster, "sudo kubeadm init phase upload-certs --upload-certs")
		certKey = regexp.MustCompile(`[a-f0-9]{64}`).FindString(uploadOutput)
		if certKey != "" {
			break
		}
		if time.Since(start) > timeout {
			log.Fatalf("Timed out after %v waiting for certificate key, last output: %q", timeout, uploadOutput)
		}
		log.Printf("Waiting for certificate key, retrying in 5s... last output: %q", uploadOutput)
		time.Sleep(5 * time.Second)
	}
	fmt.Println("certKey:", certKey)
	// 3. Join additional masters
	for _, m := range masterList[1:] {
		h := m
		joinCmd := fmt.Sprintf(
			"sudo kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s --control-plane --certificate-key %s",
			advertiseAddr, token, caHash, certKey,
		)
		fmt.Printf("Joining master node: %s\n", h)
		runCmd(h, joinCmd)
		fmt.Printf("Master %s joined.\n", h)
	}

	// 4. Join worker nodes (if any)
	if len(workerList) == 0 {
		fmt.Println("No worker nodes provided, skipping worker join.")
	} else {
		for _, w := range workerList {
			h := w
			joinWorker := fmt.Sprintf(
				"sudo kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s",
				advertiseAddr, token, caHash,
			)
			fmt.Printf("Joining worker node: %s\n", h)
			runCmd(h, joinWorker)
			fmt.Printf("Worker %s joined.\n", h)
		}
	}

	fmt.Println("Cluster creation complete.")
}

// filterEmpty trims spaces and filters out empty strings
func filterEmpty(list []string) []string {
	var res []string
	for _, s := range list {
		t := strings.TrimSpace(s)
		if t != "" {
			res = append(res, t)
		}
	}
	return res
}
