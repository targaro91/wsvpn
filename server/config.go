package main

import (
	"github.com/Doridian/wsvpn/server/servers"
	"github.com/Doridian/wsvpn/shared"
	"github.com/Doridian/wsvpn/shared/cli"
)

type Config struct {
	Tunnel struct {
		Mtu                 int    `yaml:"mtu"`
		Subnet              string `yaml:"subnet"`
		Mode                string `yaml:"mode"`
		AllowClientToClient bool   `yaml:"allow-client-to-client"`
		IpConfig            struct {
			Local  bool `yaml:"local"`
			Remote bool `yaml:"remote"`
		} `yaml:"ip-config"`
		Ping cli.PingConfig `yaml:"ping"`
	} `yaml:"tunnel"`

	Interfaces servers.InterfacesConfig `yaml:"interfaces"`

	Server struct {
		Listen      string `yaml:"listen"`
		EnableHTTP3 bool   `yaml:"enable-http3"`
		Tls         struct {
			ClientCa    string        `yaml:"client-ca"`
			Certificate string        `yaml:"certificate"`
			Key         string        `yaml:"key"`
			Config      cli.TlsConfig `yaml:"config"`
		} `yaml:"tls"`
		Authenticator struct {
			Type   string `yaml:"type"`
			Config string `yaml:"config"`
		} `yaml:"authenticator"`
	}
}

func Load(file string) *Config {
	out := &Config{}
	err := shared.LoadConfig(file, out)
	if err != nil {
		panic(err)
	}
	return out
}
