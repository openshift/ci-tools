package jobrunaggregatorapi

import (
	"encoding/json"
	"fmt"
)

type HistoricalDataRow struct {
	Name         string
	Release      string
	FromRelease  string
	Platform     string
	Architecture string
	Network      string
	Topology     string
	P95          string
	P99          string
	Type         string
}

func (c *HistoricalDataRow) UnmarshalJSON(data []byte) error {
	var values struct {
		AlertName    string
		BackendName  string
		Release      string
		FromRelease  string
		Platform     string
		Architecture string
		Network      string
		Topology     string
		P95          string
		P99          string
	}
	err := json.Unmarshal(data, &values)
	if err != nil {
		return err
	}
	c.Name = values.BackendName
	c.Type = "disruption"
	if values.AlertName != "" {
		c.Name = values.AlertName
		c.Type = "alert"
	}
	c.Release = values.Release
	c.FromRelease = values.FromRelease
	c.Platform = values.Platform
	c.Architecture = values.Architecture
	c.Network = values.Network
	c.Topology = values.Topology
	c.P95 = values.P95
	c.P99 = values.P99
	return nil
}

func (c *HistoricalDataRow) MarshalJSON() ([]byte, error) {
	var values struct {
		AlertName    string `json:",omitempty"`
		BackendName  string `json:",omitempty"`
		Release      string
		FromRelease  string
		Platform     string
		Architecture string
		Network      string
		Topology     string
		P95          string
		P99          string
	}
	if c.Type == "alerts" {
		values.AlertName = c.Name
	} else {
		values.BackendName = c.Name
	}
	values.Release = c.Release
	values.FromRelease = c.FromRelease
	values.Platform = c.Platform
	values.Architecture = c.Architecture
	values.Network = c.Network
	values.Topology = c.Topology
	values.P95 = c.P95
	values.P99 = c.P99

	return json.Marshal(values)
}

func (a *HistoricalDataRow) GetKey() string {
	return fmt.Sprintf("%s_%s_%s_%s_%s_%s_%s",
		a.Name,
		a.FromRelease,
		a.Release,
		a.Architecture,
		a.Platform,
		a.Network,
		a.Topology,
	)
}
