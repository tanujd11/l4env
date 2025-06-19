package cmd

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/spf13/cobra"
	"github.com/tanujd11/l4env/internal/config"
	"golang.org/x/crypto/ssh"
	yaml "gopkg.in/yaml.v3"
)

//go:embed assets/bgp.yaml.tpl
var bgpYaml string

//go:embed assets/cp-bgp.yaml.tpl
var cpBGPYaml string

//go:embed assets/setup.sh.tpl
var setupScript string

//go:embed assets/mitm.yaml.tpl
var mitmManifests string

var (
	masters     string
	workers     string
	sshPort     int
	sshUser     string
	sshKeyPath  string
	sshPassword string
	filePath    string
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

	cmd.Flags().StringVar(&filePath, "values-file", "", "Path to a file containing the cluster configuration (optional)")

	// Required flags
	cmd.MarkFlagRequired("masters")

	return cmd
}

func run(cmd *cobra.Command, args []string) {
	// Validate SSH auth
	if sshKeyPath == "" && sshPassword == "" {
		log.Fatalf("Either --key or --password must be provided for SSH authentication")
	}

	var conf config.ResolvedConfig
	var err error
	if filePath != "" {
		conf, err = config.ResolveConfig(filePath)
		if err != nil {
			log.Fatalf("Failed to resolve configuration file %s: %v", filePath, err)
		}
	}
	if conf.InitialMasterPrivateAddr == "" {
		conf.InitialMasterPrivateAddr = strings.Split(masters, ",")[0]
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
	if !conf.EnableKubeProxy {
		skipPhases = "--skip-phases=addon/kube-proxy"
	}
	// 1. Initialize the first master
	initCmd := fmt.Sprintf(`
if [ ! -f /etc/kubernetes/admin.conf ]; then
  sudo kubeadm init --control-plane-endpoint "%s:6443" \
    --apiserver-advertise-address %s \
    --pod-network-cidr %s %s
fi
`, conf.InitialMasterPrivateAddr, conf.InitialMasterPrivateAddr, conf.PodCIDR, skipPhases)
	fmt.Printf("Initializing primary master: %s\n", initialMaster)
	runCmd(initialMaster, initCmd)
	fmt.Printf("Primary master %s initialized.\n", initialMaster)

	// 2. Retrieve join credentials
	fmt.Println("Retrieving join credentials...")
	fmt.Println("Copying kubeconfig to /root/.kube/config...")
	runCmd(initialMaster, "mkdir -p $HOME/.kube")
	runCmd(initialMaster, "sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config")
	runCmd(initialMaster, "sudo chown $(id -u):$(id -g) $HOME/.kube/config")

	kubeProxyReplacement := "true"
	if conf.EnableKubeProxy {
		kubeProxyReplacement = "false"
	}
	// 3. Install CNI plugin (Cilium in this case)
	cniCmd := strings.Join([]string{
		"helm repo add cilium https://helm.cilium.io/",
		"helm repo update",
		"helm upgrade --install cilium cilium/cilium --version 1.17.3 " +
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
			fmt.Sprintf("--set ipam.operator.clusterPoolIPv4PodCIDRList=%s ", conf.PodCIDR) +
			"--set ipam.operator.clusterPoolIPv4MaskSize=26 " +
			"--set nodePort.enabled=true " +
			fmt.Sprintf("--set k8sServiceHost=%s ", conf.InitialMasterPrivateAddr) +
			"--set k8sServicePort=6443",
	}, " && ")

	fmt.Println("Installing CNI plugin (Cilium)...")
	runCmd(initialMaster, cniCmd)

	// check if cilium is up.
	timeout := 5 * time.Minute
	start := time.Now()
	for {
		ciliumStatus, err := runCmdAndReturnErrIfAny(initialMaster, "kubectl get pods -n kube-system -l k8s-app=cilium -o jsonpath='{.items[0].status.phase}'")
		if err != nil {
			log.Printf("Error checking Cilium status: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if ciliumStatus == "Running" {
			break
		}
		if time.Since(start) > timeout {
			log.Fatalf("Timed out after %v waiting for Cilium to be up, last status: %q", timeout, ciliumStatus)
		}
		log.Printf("Waiting for Cilium to be up, current status: %s", ciliumStatus)
		time.Sleep(5 * time.Second)
	}

	// Apply BGP configuration
	fmt.Println("Applying BGP configuration for apps...")
	bgpTmpl, err := template.New("bgpYaml").Parse(bgpYaml)
	if err != nil {
		log.Fatalf("failed to parse bgp template: %v", err)
	}
	var rendered bytes.Buffer
	err = bgpTmpl.Execute(&rendered, conf)
	if err != nil {
		log.Fatalf("failed to execute template: %v", err)
	}
	bgpCmd := fmt.Sprintf("cat <<EOF | kubectl apply -f -\n%s\nEOF", rendered.String())
	runCmd(initialMaster, bgpCmd)

	// update k8s service to LoadBalancer
	if conf.AdvertiseAddr != "" {
		// Apply Cilium BGP configuration
		fmt.Println("Applying Cilium BGP configuration. for ControlPlane..")
		bgpTmpl, err := template.New("bgpYaml").Parse(cpBGPYaml)
		if err != nil {
			log.Fatalf("failed to parse bgp template: %v", err)
		}
		var rendered bytes.Buffer
		err = bgpTmpl.Execute(&rendered, conf)
		cpBGPCmd := fmt.Sprintf("cat <<EOF | kubectl apply -f -\n%s\nEOF", rendered.String())
		runCmd(initialMaster, cpBGPCmd)

		fmt.Println("Updating Kubernetes service to LoadBalancer...")
		updateServiceCmd := fmt.Sprintf(`kubectl -n default patch svc kubernetes -p '{"spec": {"type": "LoadBalancer", "loadBalancerIP": "%s"}}'`, conf.AdvertiseAddr)
		runCmd(initialMaster, updateServiceCmd)
	}

	// update kubeadm config to use advertiseAddr
	if conf.AdvertiseAddr != "" && conf.UseAdvertisedAddrInKubeadm {
		fmt.Println("Updating kubeadm config to use advertiseAddr...")
		clusterConfig := runCmd(initialMaster, "kubectl -n kube-system get configmap kubeadm-config -o jsonpath='{.data.ClusterConfiguration}'")

		// Parse YAML as map for flexibility
		var cc map[string]interface{}
		if err := yaml.Unmarshal([]byte(clusterConfig), &cc); err != nil {
			log.Fatalf("unmarshal error: %w", err)
		}
		cc["controlPlaneEndpoint"] = fmt.Sprintf("%s:6443", conf.InitialMasterPrivateAddr)
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
	}

	// Install MITMProxy manifests
	fmt.Println("Installing MITMProxy manifests...")
	mitmManifestsTpl, err := template.New("mitmManifests").Parse(mitmManifests)
	if err != nil {
		log.Fatalf("failed to parse mitmproxy template: %v", err)
	}
	var renderedManifests bytes.Buffer
	err = mitmManifestsTpl.Execute(&renderedManifests, conf)
	mitmManifestsCmd := fmt.Sprintf("cat <<'EOF' | kubectl apply -f -\n%s\nEOF\n", renderedManifests.String())
	runCmd(initialMaster, mitmManifestsCmd)

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
	// 3. Join additional masters
	for _, m := range masterList[1:] {
		h := m
		joinCmd := fmt.Sprintf(`
if [ ! -f /etc/kubernetes/admin.conf ]; then
sudo kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s --control-plane --certificate-key %s
fi
`, conf.InitialMasterPrivateAddr, token, caHash, certKey)
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
			joinWorker := fmt.Sprintf(`
if [ ! -f /etc/kubernetes/kubelet.conf ]; then
sudo kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s
fi
`, conf.InitialMasterPrivateAddr, token, caHash)
			fmt.Printf("Joining worker node: %s\n", h)
			runCmd(h, joinWorker)

			// Adding IP Table rules on worker nodes
			setupShTPL, err := template.New("setupsh").Parse(setupScript)
			if err != nil {
				log.Fatalf("failed to parse setup script template: %v", err)
			}
			var rendered bytes.Buffer
			err = setupShTPL.Execute(&rendered, struct {
				MITMVIP string
			}{
				MITMVIP: conf.MITMVIP,
			})
			if err != nil {
				log.Fatalf("failed to execute template: %v", err)
			}
			runCmd(h, fmt.Sprintf("cat <<'EOF' | sudo tee /tmp/setup.sh >/dev/null\n%s\nEOF", rendered.String()))
			runCmd(h, "sudo chmod +x /tmp/setup.sh")
			runCmd(h, "sudo bash /tmp/setup.sh")
			fmt.Printf("Worker %s joined.\n", h)
		}
	}

	// label node-role to all the workers
	nodeRoleCmd := "kubectl get nodes --selector='!node-role.kubernetes.io/control-plane' -o name | xargs -I{} kubectl label {} node-role.kubernetes.io/worker="
	runCmd(initialMaster, nodeRoleCmd)
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
