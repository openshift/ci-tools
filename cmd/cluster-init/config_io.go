package main

import (
	"io/ioutil"
	"sigs.k8s.io/yaml"
)

func loadConfig(filename string, o interface{}) {
	data, err := ioutil.ReadFile(filename)
	check(err, "cannot open config file: ", filename)
	err = yaml.Unmarshal(data, o)
	check(err, "cannot unmarshall config file: ", filename)
}

func saveConfig(filename string, o interface{}) {
	y, err := yaml.Marshal(o)
	check(err, "cannot marshal InfraPeriodics")

	err = ioutil.WriteFile(filename, y, 0644)
	check(err, "cannot write InfraPeriodics file: ", filename)

}
