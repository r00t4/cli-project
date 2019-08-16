package lib

import (
	"encoding/json"
	"io/ioutil"
)

type Config struct {
	Interface string`json:interface`
	Upstreams []Upstream`json:"upstreams"`
}

type Upstream struct {
	Path string`json:"path"`
	Method string`json:"method"`
	Backends []string`json:"backends"`
	ProxyMethod string`json:"proxyMethod"`
}

func GetConfig(filename string) ([]Config, error) {
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		return []Config{}, err
	}
	var data []Config
	_= json.Unmarshal(file, &data)

	return data, nil
}

