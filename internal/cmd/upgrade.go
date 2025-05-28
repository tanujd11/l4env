package cmd

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"
)

// UpgradeCommand returns the upgrade command for cluster upgrade
func UpgradeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade Kubernetes cluster to a new version",
		Run:   runUpgrade,
	}
	cmd.Flags().StringVar(&masters, "masters", "", "Comma-separated list of master node IPs (first is primary)")
	cmd.Flags().StringVar(&workers, "workers", "", "Comma-separated list of worker node IPs (optional)")
	cmd.Flags().IntVar(&sshPort, "port", 22, "SSH port")
	cmd.Flags().StringVar(&sshUser, "user", "root", "SSH user")
	cmd.Flags().StringVar(&sshKeyPath, "key", "", "Path to private key file")
	cmd.Flags().StringVar(&sshPassword, "password", "", "SSH password (optional)")

	cmd.MarkFlagRequired("masters")
	cmd.MarkFlagRequired("key")
	return cmd
}

func runUpgrade(cmd *cobra.Command, args []string) {
	if sshKeyPath == "" && sshPassword == "" {
		log.Fatal("Either --key or --password must be provided for SSH authentication")
	}

	mList := filterEmpty(strings.Split(masters, ","))
	initial, err := findPrimary(mList)
	if err != nil {
		log.Fatalf("Failed to find primary master: %v", err)
	}
	wList := filterEmpty(strings.Split(workers, ","))

	// 2. Plan and apply upgrade
	runCmd(initial, "sudo kubeadm upgrade plan")
	runCmd(initial, "sudo kubeadm upgrade apply -y")

	// 3. Upgrade kubelet & kubectl on primary master
	runCmd(initial, "sudo systemctl daemon-reload && sudo systemctl restart kubelet")

	// 4. Upgrade additional masters
	for _, m := range mList[1:] {
		fmt.Printf("Upgrading kubeadm on additional master %s...\n", m)
		runCmd(m, "sudo systemctl daemon-reload && sudo systemctl restart kubelet")
	}

	// 5. Upgrade workers
	for _, w := range wList {
		fmt.Printf("Upgrading node %s...\n", w)
		runCmd(w, "sudo systemctl daemon-reload && sudo systemctl restart kubelet")
	}
}
