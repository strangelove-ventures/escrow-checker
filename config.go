package main

type Config struct {
	Chains []ChainConfig `yaml:"chains" json:"chains"`
}
type ChainConfig struct {
	Name          string `yaml:"name" json:"name"`
	ChainID       string `yaml:"chain-id" json:"chain-id"`
	RPCAddress    string `yaml:"rpc-address" json:"rpc-address"`
	AccountPrefix string `yaml:"account-prefix" json:"account-prefix"`
	Timeout       string `yaml:"timeout" json:"timeout"`
}
