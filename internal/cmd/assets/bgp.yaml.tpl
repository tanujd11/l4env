apiVersion: "cilium.io/v2alpha1"
kind: CiliumLoadBalancerIPPool
metadata:
  name: pool
spec:
  blocks:
  - cidr: {{ .LoadBalancerIPPoolCIDR }}
---
apiVersion: cilium.io/v2alpha1
kind: CiliumBGPClusterConfig
metadata:
  name: bgp-internal
spec:
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
  bgpInstances:
  - name: internal
    localASN: 64512
    peers:
    - name: frr-router
      peerASN: {{ .BGPPeerASN }}
      peerAddress: {{ .BGPPeerAddress }}
      peerConfigRef:
        name: cilium-peer
---
apiVersion: cilium.io/v2alpha1
kind: CiliumBGPPeerConfig
metadata:
  name: cilium-peer
spec:
  families:
  - afi: ipv4
    safi: unicast
    advertisements:
      matchLabels:
        advertise: "bgp"
  gracefulRestart:
    enabled: true
    restartTimeSeconds: 15
---
apiVersion: cilium.io/v2alpha1
kind: CiliumBGPAdvertisement
metadata:
  name: bgp-advertisements
  labels:
    advertise: bgp
spec:
  advertisements:
  - advertisementType: "Service"
    service:
      addresses:
      - LoadBalancerIP
    selector:
      matchExpressions:
      # All services
      - {key: component, operator: NotIn, values: ['apiserver']}
      - {key: somekey, operator: NotIn, values: ['never-used-value']}
