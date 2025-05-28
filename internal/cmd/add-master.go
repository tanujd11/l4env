package cmd

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tanujd11/l4env/internal/config"
	"golang.org/x/crypto/ssh"
)

var (
	primary    string
	newMasters string
)

// AddMasterCommand returns a command to add new control-plane nodes
func AddMasterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-master",
		Short: "Add one or more master nodes to an existing cluster",
		Run: func(cmd *cobra.Command, args []string) {
			// Validate flags
			if sshKeyPath == "" && sshPassword == "" {
				log.Fatal("Either --key or --password must be provided for SSH authentication")
			}

			var conf config.ResolvedConfig
			var err error
			if filePath != "" {
				conf, err = config.ResolveConfig(filePath)
				if err != nil {
					log.Fatalf("Failed to resolve configuration file %s: %v", filePath, err)
				}
			}
			// Parse lists
			pm := filterEmpty(strings.Split(primary, ","))
			nm := filterEmpty(strings.Split(newMasters, ","))
			initial, err := findPrimary(pm)
			if err != nil {
				log.Fatalf("Failed to find primary master: %v", err)
			}

			// Retrieve join command
			joinOutput := runCmd(initial, "sudo kubeadm token create --print-join-command")
			fields := strings.Fields(joinOutput)
			var token, caHash string
			for i, f := range fields {
				if f == "--token" && i+1 < len(fields) {
					token = fields[i+1]
				}
				if f == "--discovery-token-ca-cert-hash" && i+1 < len(fields) {
					caHash = fields[i+1]
				}
			}
			if token == "" || caHash == "" {
				log.Fatalf("Failed to parse token or CA hash from: %q", joinOutput)
			}

			// Retrieve certificate key
			uploadOutput := runCmd(initial, "sudo kubeadm init phase upload-certs --upload-certs")
			certKey := regexp.MustCompile(`[a-f0-9]{64}`).FindString(uploadOutput)
			if certKey == "" {
				log.Fatalf("Failed to parse certificate key from: %q", uploadOutput)
			}

			// Join new masters
			for _, m := range nm {
				joinCmd := fmt.Sprintf(
					"sudo kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s --control-plane --certificate-key %s --control-plane-endpoint %s:6443",
					conf.AdvertiseAddr, token, caHash, certKey, conf.AdvertiseAddr,
				)
				runCmd(m, joinCmd)
				fmt.Printf("Master %s joined as control-plane.\n", m)
			}
		},
	}

	// Flags
	cmd.Flags().StringVar(&primary, "primary", "", "Comma-separated list of existing master nodes")
	cmd.Flags().StringVar(&newMasters, "masters", "", "Comma-separated list of new master node IPs/hosts to add")
	cmd.Flags().IntVar(&sshPort, "port", 22, "SSH port")
	cmd.Flags().StringVar(&sshUser, "user", "root", "SSH user")
	cmd.Flags().StringVar(&sshKeyPath, "key", "", "Path to private key file")
	cmd.Flags().StringVar(&sshPassword, "password", "", "SSH password (optional)")

	// Required
	cmd.MarkFlagRequired("primary")
	cmd.MarkFlagRequired("masters")
	cmd.MarkFlagRequired("control-plane-endpoint")
	cmd.MarkFlagRequired("key")

	return cmd
}

func findPrimary(masters []string) (string, error) {
	if len(masters) == 0 {
		return "", fmt.Errorf("no masters provided")
	}
	sshCfg := SSHClientConfig()
	var primary string
	for _, host := range masters {
		client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, sshPort), sshCfg)
		if err != nil {
			log.Printf("SSH to %s failed: %v, skipping", host, err)
			continue
		}
		client.Close()
		out := runCmd(host, "sudo test -f /etc/kubernetes/admin.conf && echo ok")
		if strings.TrimSpace(out) == "ok" {
			primary = host
			break
		}
	}
	if primary == "" {
		return "", fmt.Errorf("no primary master found in the provided list")
	}

	return primary, nil
}
