package lib

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

// struct for Configuration File
type Config struct {
	Interface string     `json:interface` // interface such as ':8080'
	Upstreams []Upstream `json:"upstreams"`
}

type Upstream struct {
	Path        string   `json:"path"` // some path
	Method      string   `json:"method"` // get, post ... get
	Backends    []string `json:"backends"` // urls to send requests
	ProxyMethod string   `json:"proxyMethod"` // anycast / round-robin
}

// returns array of Configs using file
func GetConfig(filename string) ([]Config, error) {
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		return []Config{}, err
	}
	var data []Config
	_ = json.Unmarshal(file, &data)

	return data, nil
}

// to string method for Config
func (c *Config) String() string {
	return fmt.Sprintf("%s", c)
}
