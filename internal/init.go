package internal

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var (
	// SSH flags
	nodes          string
	kubeadmVersion string
)

// SSHClientConfig builds SSH client configuration
func SSHClientConfig() *ssh.ClientConfig {
	methods := []ssh.AuthMethod{}
	if sshKeyPath != "" {
		buf, err := os.ReadFile(sshKeyPath)
		if err != nil {
			log.Fatalf("Failed to read private key file: %v", err)
		}
		signer, err := ssh.ParsePrivateKey(buf)
		if err != nil {
			log.Fatalf("Failed to parse private key: %v", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if sshPassword != "" {
		methods = append(methods, ssh.Password(sshPassword))
	}
	return &ssh.ClientConfig{
		User:            sshUser,
		Auth:            methods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
}

// runCmd executes a command over SSH on the given host
func runCmd(host, command string) string {
	addr := fmt.Sprintf("%s:%d", host, sshPort)
	client, err := ssh.Dial("tcp", addr, SSHClientConfig())
	if err != nil {
		log.Fatalf("SSH dial to %s failed: %v", host, err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		log.Fatalf("SSH session to %s failed: %v", host, err)
	}
	defer sess.Close()

	var buf bytes.Buffer
	sess.Stdout = &buf
	sess.Stderr = &buf
	if err := sess.Run(command); err != nil {
		log.Fatalf("Command '%s' on %s failed: %v\nOutput: %s", command, host, err, buf.String())
	}
	return strings.TrimSpace(buf.String())
}

// installPrereqs installs kubeadm prerequisites and kubeadm itself on host
func installPrereqs(host string) {
	cmds := []string{
		"sudo swapoff -a",
		"sudo sed -i.bak '/ swap / s/^/#/' /etc/fstab",
		"sudo modprobe br_netfilter",
		"sudo sysctl -w net.bridge.bridge-nf-call-iptables=1",
		"sudo sysctl -w net.bridge.bridge-nf-call-ip6tables=1",
		"sudo apt-get update",
		"sudo apt-get install -y ca-certificates curl gnupg",
		// Configure containerd to use SystemdCgroup
		`cat << 'EOF' | sudo tee /etc/containerd/config.toml > /dev/null
version = 2
[plugins]
  [plugins."io.containerd.grpc.v1.cri"]
    [plugins."io.containerd.grpc.v1.cri".containerd]
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes]
        [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
          runtime_type = "io.containerd.runc.v2"
          [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
            SystemdCgroup = true
EOF`,
		"sudo systemctl restart containerd",
		"curl -fsSL https://build.opensuse.org/projects/isv:kubernetes/signing_keys/download?kind=gpg | sudo gpg --yes --dearmor -o /etc/apt/keyrings/kubernetes-archive-keyring.gpg",
		fmt.Sprintf("echo \"deb [signed-by=/etc/apt/keyrings/kubernetes-archive-keyring.gpg] https://pkgs.k8s.io/core:/stable:/%s/deb/ /\" | sudo tee /etc/apt/sources.list.d/kubernetes.list > /dev/null", kubeadmVersion),
		"sudo apt-get update",
		`sudo apt-get install -y -o Dpkg::Options::="--force-overwrite" --no-install-recommends kubelet kubeadm kubectl`,
		"sudo apt-mark hold kubelet kubeadm kubectl",
		"curl -fsSL https://get.helm.sh/helm-v3.11.3-linux-amd64.tar.gz | tar xz -C /tmp && sudo mv /tmp/linux-amd64/helm /usr/local/bin/helm && rm -rf /tmp/linux-amd64",
	}
	fmt.Printf("Installing prerequisites on %s...\n", host)
	for _, c := range cmds {
		runCmd(host, c)
	}
}

// InitCommand returns the init command for installing prerequisites
func InitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install prerequisites and kubeadm on all nodes",
		Run:   runInit,
	}
	cmd.Flags().StringVar(&nodes, "nodes", "", "Comma-separated list of node IPs/hostnames")
	cmd.Flags().IntVar(&sshPort, "port", 22, "SSH port")
	cmd.Flags().StringVar(&sshUser, "user", "root", "SSH user")
	cmd.Flags().StringVar(&sshKeyPath, "key", "", "Path to private key file")
	cmd.Flags().StringVar(&sshPassword, "password", "", "SSH password (optional)")
	cmd.Flags().StringVar(&kubeadmVersion, "kubeadm-version", "v1.27", "Kubeadm version to install")
	cmd.MarkFlagRequired("nodes")
	cmd.MarkFlagRequired("kubeadm-version")
	return cmd
}

func runInit(cmd *cobra.Command, args []string) {
	if sshKeyPath == "" && sshPassword == "" {
		log.Fatal("Either --key or --password must be provided for SSH authentication")
	}
	nList := filterEmpty(strings.Split(nodes, ","))
	wg := &sync.WaitGroup{}
	for _, h := range nList {
		wg.Add(1)
		go func() {
			installPrereqs(h)
			fmt.Printf("Prerequisites installed on %s.\n", h)
			wg.Done()
		}()
	}
	wg.Wait()
}
