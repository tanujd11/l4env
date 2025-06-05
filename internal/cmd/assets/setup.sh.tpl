#!/bin/bash

ENVOY_PORT="12001"          
ENVOY_MARK="1"              
POD_ENVOY_UID="101"         
ROUTE_TABLE="100"
TUN_IF="mitm-tunnel"          
MAIN_IF=$(ip -o -4 route show to default | awk '{print $5}' | cut -d/ -f1)
INSTANCE_IP=$(ip -o -4 addr show ${MAIN_IF} | awk '{print $4}' | cut -d/ -f1)
# ────────────────────────────────────────────────────────────

sudo ip tunnel add ${TUN_IF} mode ipip local {{ .MITMVIP }}
sudo ip link set ${TUN_IF} up
sudo ip addr add {{ .MITMVIP }}/32 dev ${TUN_IF}

echo "==> using TUN_IF=${TUN_IF} MAIN_IF=${MAIN_IF} INSTANCE_IP=${INSTANCE_IP}"

apply_sysctl() {
  local key=$1 val=$2
  current=$(sysctl -n "$key")
  if [[ "$current" != "$val" ]]; then
    echo " - sysctl $key=$val (was $current)"
    sudo sysctl -w "$key=$val" >/dev/null
  fi
}

echo "==> apply sysctl"
apply_sysctl net.ipv4.ip_forward 1
apply_sysctl net.ipv4.conf.all.rp_filter 0
apply_sysctl net.ipv4.conf.default.rp_filter 0

echo "==> set policy-routing"
sudo ip rule del fwmark ${ENVOY_MARK} lookup ${ROUTE_TABLE} 2>/dev/null || true
sudo ip route flush table ${ROUTE_TABLE} 2>/dev/null || true
sudo ip route add local default dev lo table ${ROUTE_TABLE}
sudo ip rule add fwmark ${ENVOY_MARK} lookup ${ROUTE_TABLE}

TABLES=(mangle filter nat)
for tbl in "${TABLES[@]}"; do
  for chain in $(sudo iptables -t "$tbl" -S | awk '/MITM_/ {print $2}' | sort -u); do
    echo " - flush $tbl/$chain"
    sudo iptables -t "$tbl" -F "$chain" 2>/dev/null || true
  done
done

sudo iptables -t mangle -D PREROUTING -p tcp -j MITM_MANGLE_PREROUTING 2>/dev/null || true
sudo iptables -t mangle -D OUTPUT     -p tcp -j MITM_MANGLE_OUTPUT      2>/dev/null || true
sudo iptables -t filter -D INPUT   -j MITM_FILTER_INPUT   2>/dev/null || true
sudo iptables -t filter -D FORWARD -j MITM_FILTER_FORWARD 2>/dev/null || true

for tbl in "${TABLES[@]}"; do
  for chain in MITM_MANGLE_PREROUTING MITM_MANGLE_OUTPUT MITM_MANGLE_TPROXY \
               MITM_FILTER_INPUT MITM_FILTER_FORWARD; do
    sudo iptables -t "$tbl" -X "$chain" 2>/dev/null || true
  done
done

sudo iptables -t mangle -N MITM_MANGLE_PREROUTING
sudo iptables -t mangle -N MITM_MANGLE_OUTPUT
sudo iptables -t mangle -N MITM_MANGLE_TPROXY
sudo iptables -t filter -N MITM_FILTER_INPUT
sudo iptables -t filter -N MITM_FILTER_FORWARD

sudo iptables -t mangle -I PREROUTING 1 \
    -m conntrack --ctstate ESTABLISHED,RELATED \
    -j CONNMARK --restore-mark

sudo iptables -t mangle -I PREROUTING 2 -p tcp -j MITM_MANGLE_PREROUTING

sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -i lo -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -p tcp --dport 22 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -p tcp --dport ${ENVOY_PORT} -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -p tcp --dport 179 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -d 10.0.0.0/8 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -d 192.0.0.0/8 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -d 172.0.0.0/8 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -p tcp --dport 53 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING -p tcp --dport 10250 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_PREROUTING \
     -i ${TUN_IF} -p tcp ! -d ${INSTANCE_IP}/32 -j MITM_MANGLE_TPROXY

sudo iptables -t mangle -A MITM_MANGLE_TPROXY \
     -p tcp -j TPROXY --on-port ${ENVOY_PORT} \
     --tproxy-mark ${ENVOY_MARK}/0x${ENVOY_MARK}
sudo iptables -t mangle -A MITM_MANGLE_TPROXY -j CONNMARK --save-mark

sudo iptables -t mangle -I OUTPUT 1 -p tcp -j MITM_MANGLE_OUTPUT

sudo iptables -t mangle -A MITM_MANGLE_OUTPUT \
     -m owner --uid-owner ${POD_ENVOY_UID} -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -o lo -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -p tcp --dport 22 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -p tcp --dport 179 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -d 10.0.0.0/8 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -d 172.0.0.0/8 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -d 192.0.0.0/8 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -p tcp --dport 53 -j RETURN
sudo iptables -t mangle -A MITM_MANGLE_OUTPUT -p tcp --dport 10250 -j RETURN

if ! sudo iptables -t nat -C POSTROUTING -o ${MAIN_IF} -j MASQUERADE 2>/dev/null; then
  sudo iptables -t nat -A POSTROUTING -o ${MAIN_IF} -j MASQUERADE
fi

sudo iptables -t filter -I INPUT   1 -j MITM_FILTER_INPUT
sudo iptables -t filter -I FORWARD 1 -j MITM_FILTER_FORWARD

sudo iptables -t filter -A MITM_FILTER_INPUT -i lo -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -p tcp --dport 22 -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -p tcp --dport ${ENVOY_PORT} -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -m mark --mark ${ENVOY_MARK} -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -d 10.0.0.0/8 -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -d 192.0.0.0/8 -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -d 172.0.0.0/8 -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -p tcp --dport 53 -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_INPUT -p tcp --dport 10250 -j ACCEPT
# Kubelet
sudo iptables -t filter -I MITM_FILTER_INPUT -p tcp --dport 10250 -j ACCEPT
# Ping
sudo iptables -t filter -A MITM_FILTER_INPUT -p icmp -j ACCEPT
# Allow tunnel traffic
sudo iptables -t filter -I MITM_FILTER_INPUT 3 -p 4 -j ACCEPT
# VXLAN (UDP 8472)
sudo iptables -t filter -A MITM_FILTER_INPUT -p udp --dport 8472 -j ACCEPT
# Geneve (UDP 6081)
sudo iptables -t filter -A MITM_FILTER_INPUT -p udp --dport 6081 -j ACCEPT
# Drop everything else
sudo iptables -t filter -A MITM_FILTER_INPUT -j DROP   

sudo iptables -t filter -A MITM_FILTER_FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_FORWARD -m mark --mark ${ENVOY_MARK} -j ACCEPT
sudo iptables -t filter -A MITM_FILTER_FORWARD -j DROP