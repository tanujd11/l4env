dev.newawsenv:
	GOOS=linux GOARCH=amd64 go build -o cloud/aws/bin/l4env_amd64
	terraform -chdir=cloud/aws init -upgrade
	terraform -chdir=cloud/aws apply -var-file=tfvars/stage.tfvars

dev.destroyawsenv:
	terraform -chdir=cloud/aws destroy -var-file=tfvars/stage.tfvars
