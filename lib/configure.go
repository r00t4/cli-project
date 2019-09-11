package lib

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

// configure.go used for getting configs from config file.

// Config describes content of configuration file.
// Interface used for describe on which port will run server.
type Config struct {
	Interface string     `json:interface` // interface such as ':8080'
	Upstreams []Upstream `json:"upstreams"`
}

// Upstream describes what will do path.
// Which method will be used, source urls and proxy method.
// Proxy methods can be: anycast, round-robin and rabbitmq
type Upstream struct {
	Path        string   `json:"path"` // some path
	Method      string   `json:"method"` // get, post ... get
	Backends    []string `json:"backends"` // urls to send requests
	ProxyMethod string   `json:"proxyMethod"`
}

// This method returns array of Configs using filename
func GetConfig(filename string) ([]Config, error) {
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		return []Config{}, err
	}
	var data []Config
	_ = json.Unmarshal(file, &data)

	return data, nil
}

// toString method for Config
func (c *Config) String() string {
	return fmt.Sprintf("%s", c)
}
