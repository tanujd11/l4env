#!/bin/bash
set -e

# Read input JSON from stdin (required by terraform external)
eval "$(jq -r '@sh "ASG_NAME=\(.asg_name) EXPECTED_COUNT=\(.expected_count) REGION=\(.region)"')"

while true; do
  INSTANCE_IDS=$(aws autoscaling describe-auto-scaling-groups \
    --auto-scaling-group-names "$ASG_NAME" \
    --region "$REGION" \
    --query "AutoScalingGroups[0].Instances[*].InstanceId" \
    --output text)

  if [ -z "$INSTANCE_IDS" ]; then
    sleep 5
    continue
  fi

  IPS=$(aws ec2 describe-instances \
    --instance-ids $INSTANCE_IDS \
    --region "$REGION" \
    --query "Reservations[*].Instances[*].PrivateIpAddress" \
    --output text | tr '\t' '\n' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | sort)

  COUNT=$(echo "$IPS" | grep -c '^')
  if [ "$COUNT" -eq "$EXPECTED_COUNT" ]; then
    IPS_JSON=$(echo "$IPS" | jq -R . | jq -c -s .)
    jq -n --arg ips "$IPS_JSON" '{"private_ips": $ips}'
    exit 0
  fi
  sleep 5
done
