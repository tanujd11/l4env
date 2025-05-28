apiVersion: cilium.io/v2alpha1
kind: CiliumBGPClusterConfig
metadata:
  name: bgp-internal-control-plane
spec:
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/control-plane: ""
  bgpInstances:
  - name: internal
    localASN: 64512
    peers:
    - name: frr-router
      peerASN: {{ .BGPPeerASN }}
      peerAddress: {{ .BGPPeerAddress }}
      peerConfigRef:
        name: cilium-peer-control-plane
---
apiVersion: cilium.io/v2alpha1
kind: CiliumBGPPeerConfig
metadata:
  name: cilium-peer-control-plane
spec:
  families:
  - afi: ipv4
    safi: unicast
    advertisements:
      matchLabels:
        advertise: "control-plane"
  gracefulRestart:
    enabled: true
    restartTimeSeconds: 15
---
apiVersion: cilium.io/v2alpha1
kind: CiliumBGPAdvertisement
metadata:
  name: bgp-advertisements-cp
  labels:
    advertise: control-plane
spec:
  advertisements:
  - advertisementType: "Service"
    service:
      addresses:
      - LoadBalancerIP
    selector:
      matchExpressions:
      - {key: component, operator: In, values: ['apiserver']}
