package cmd

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tanujd11/l4env/internal/config"
)

var (
	newWorkers string
)

// AddWorkerCommand returns a command to add new worker nodes
func AddWorkerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-worker",
		Short: "Add one or more worker nodes to an existing cluster",
		Run: func(cmd *cobra.Command, args []string) {
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

			wm := filterEmpty(strings.Split(newWorkers, ","))
			// Retrieve join command
			joinOutput := runCmd(primary, "sudo kubeadm token create --print-join-command")
			fields := strings.Fields(joinOutput)
			fmt.Println("Join command:", joinOutput)
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

			// Join new workers
			for _, w := range wm {
				joinCmd := fmt.Sprintf(
					"sudo kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s",
					conf.AdvertiseAddr, token, caHash,
				)
				runCmd(w, joinCmd)
				fmt.Printf("Worker %s joined.\n", w)
			}
			// label node-role to all the workers
			nodeRoleCmd := "kubectl get nodes --selector='!node-role.kubernetes.io/control-plane' -o name | xargs -I{} kubectl label {} node-role.kubernetes.io/worker=	"
			runCmd(primary, nodeRoleCmd)
			fmt.Println("Cluster creation complete.")
		},
	}

	cmd.Flags().StringVar(&primary, "primary", "", "API endpoint or IP of the control-plane to retrieve join token")
	cmd.Flags().StringVar(&newWorkers, "workers", "", "Comma-separated list of new worker node IPs/hosts to add")
	cmd.Flags().IntVar(&sshPort, "port", 22, "SSH port")
	cmd.Flags().StringVar(&sshUser, "user", "root", "SSH user")
	cmd.Flags().StringVar(&sshKeyPath, "key", "", "Path to private key file")
	cmd.Flags().StringVar(&sshPassword, "password", "", "SSH password (optional)")

	cmd.MarkFlagRequired("primary")
	cmd.MarkFlagRequired("workers")
	cmd.MarkFlagRequired("key")

	return cmd
}
