/*
Copyright (c) Tetrate, Inc 2025 All Rights Reserved.
*/

package config

import (
	"fmt"

	"github.com/spf13/viper"
)

var (
	defaultPodCIDR = "10.42.0.0/16"
)

type Config struct {
	// podCIDR to determine podCIDR for the cluster
	PodCIDR string `mapstructure:"podCIDR"`
	// enableKubeProxy to determine if kube-proxy should be enabled
	EnableKubeProxy bool `mapstructure:"enableKubeProxy"`
	// AdvertiseAddr is the address to advertise for the control plane
	AdvertiseAddr string `mapstructure:"advertiseAddr"`
	// InitialMasterPrivateAddr is the private address of the initial master node
	InitialMasterPrivateAddr string `mapstructure:"initialMasterPrivateAddr"`
	// InitialMasterPublicAddr is the public address of the initial master node
	UseAdvertisedAddrInKubeadm bool `mapstructure:"useAdvertisedAddrInKubeadm"`
	// LoadBalancerIPPoolCIDR is the CIDR for the load balancer IP pool
	LoadBalancerIPPoolCIDR string `mapstructure:"loadBalancerIPPoolCIDR"`
	// BGPPeerAddress is the address of the BGP peer
	BGPPeerAddress string `mapstructure:"bgpPeerAddress"`
	// BGPPeerASN is the ASN of the BGP peer
	BGPPeerASN int `mapstructure:"bgpPeerASN"`
	// MITMVIP is the VIP for the MITM service
	MITMVIP string `mapstructure:"mitmVIP"`
	// MITMSecretConfig
	MITMSecretConfig string `mapstructure:"mitmSecretConfig"`
	// EnvoyImage is the image to use for Envoy
	EnvoyImage string `mapstructure:"envoyImage"`
	// ExtProcImage is the image to use for the external processor
	ExtProcImage string `mapstructure:"extProcImage"`
	// ImagePullSecretData is the image pull secret to use for mitm images
	ImagePullSecretData string `mapstructure:"imagePullSecretData"`
}

type ResolvedConfig struct {
	// PodCIDR to determine podCIDR for the cluster
	PodCIDR string
	// EnableKubeProxy to determine if kube-proxy should be enabled
	EnableKubeProxy bool
	// AdvertiseAddr is the address to advertise for the control plane
	AdvertiseAddr string
	// InitialMasterPrivateAddr is the private address of the initial master node
	InitialMasterPrivateAddr string
	// InitialMasterPublicAddr is the public address of the initial master node
	UseAdvertisedAddrInKubeadm bool
	// LoadBalancerIPPoolCIDR is the CIDR for the load balancer IP pool
	LoadBalancerIPPoolCIDR string
	// BGPPeerAddress is the address of the BGP peer
	BGPPeerAddress string
	// BGPPeerASN is the ASN of the BGP peer
	BGPPeerASN int
	// MITMVIP is the VIP for the MITM service
	MITMVIP string
	// MITMSecretConfig is the secret config for the MITM service
	MITMSecretConfig string
	// EnvoyImage is the image to use for Envoy
	EnvoyImage string
	// ExtProcImage is the image to use for the external processor
	ExtProcImage string
	// ImagePullSecretData is the image pull secret to use for mitm images
	ImagePullSecretData string
}

func ResolveConfig(configFilePath string) (ResolvedConfig, error) {
	viper.SetConfigFile(configFilePath) // Set the name of the config file

	err := viper.ReadInConfig()
	if err != nil {
		return ResolvedConfig{}, err
	}

	viper.SetDefault("podCIDR", defaultPodCIDR)
	viper.SetDefault("enableKubeProxy", false)
	viper.SetDefault("advertiseAddr", "")
	viper.SetDefault("initialMasterPrivateAddr", "")
	viper.SetDefault("useAdvertisedAddrInKubeadm", false)

	var config Config
	err = viper.Unmarshal(&config)
	if err != nil {
		return ResolvedConfig{}, err
	}

	resolvedConfig := ResolvedConfig{
		PodCIDR:                    config.PodCIDR,
		EnableKubeProxy:            config.EnableKubeProxy,
		AdvertiseAddr:              config.AdvertiseAddr,
		InitialMasterPrivateAddr:   config.InitialMasterPrivateAddr,
		UseAdvertisedAddrInKubeadm: config.UseAdvertisedAddrInKubeadm,
		LoadBalancerIPPoolCIDR:     config.LoadBalancerIPPoolCIDR,
		BGPPeerAddress:             config.BGPPeerAddress,
		BGPPeerASN:                 config.BGPPeerASN,
		MITMVIP:                    config.MITMVIP,
		MITMSecretConfig:           config.MITMSecretConfig,
		EnvoyImage:                 config.EnvoyImage,
		ExtProcImage:               config.ExtProcImage,
		ImagePullSecretData:        config.ImagePullSecretData,
	}

	err = validateConfig(resolvedConfig)
	if err != nil {
		return ResolvedConfig{}, fmt.Errorf("invalid config: %w", err)
	}

	return resolvedConfig, nil
}

// TODO: validate config
func validateConfig(config ResolvedConfig) error {
	return nil
}
