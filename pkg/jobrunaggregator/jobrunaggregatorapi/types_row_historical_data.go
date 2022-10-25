package jobrunaggregatorapi

import (
	"fmt"
)

type HistoricalData interface {
	GetJobData() HistoricalJobData
	GetName() string
	GetP99() string
	GetP95() string
	GetKey() string
	GetJobRuns() int
}

type HistoricalJobData struct {
	Release      string
	FromRelease  string
	Platform     string
	Architecture string
	Network      string
	Topology     string
	// JobRuns is the number of job runs that were included when we queried the historical data.
	JobRuns int
}

type AlertHistoricalDataRow struct {
	AlertName string
	HistoricalJobData
	P95     string
	P99     string
	JobRuns int
}

func (a *AlertHistoricalDataRow) GetJobData() HistoricalJobData {
	return a.HistoricalJobData
}
func (a *AlertHistoricalDataRow) GetName() string {
	return a.AlertName
}
func (a *AlertHistoricalDataRow) GetP99() string {
	return a.P99
}
func (a *AlertHistoricalDataRow) GetP95() string {
	return a.P95
}
func (a *AlertHistoricalDataRow) GetJobRuns() int {
	return a.JobRuns
}
func (a *AlertHistoricalDataRow) GetKey() string {
	return fmt.Sprintf("%s_%s_%s_%s_%s_%s_%s",
		a.AlertName,
		a.FromRelease,
		a.Release,
		a.Architecture,
		a.Platform,
		a.Network,
		a.Topology,
	)
}

type DisruptionHistoricalDataRow struct {
	BackendName string
	HistoricalJobData
	P95 string
	P99 string
}

func (a *DisruptionHistoricalDataRow) GetJobData() HistoricalJobData {
	return a.HistoricalJobData
}
func (a *DisruptionHistoricalDataRow) GetName() string {
	return a.BackendName
}
func (a *DisruptionHistoricalDataRow) GetP99() string {
	return a.P99
}
func (a *DisruptionHistoricalDataRow) GetP95() string {
	return a.P95
}
func (a *DisruptionHistoricalDataRow) GetJobRuns() int {
	return a.JobRuns
}
func (a *DisruptionHistoricalDataRow) GetKey() string {
	return fmt.Sprintf("%s_%s_%s_%s_%s_%s_%s",
		a.BackendName,
		a.FromRelease,
		a.Release,
		a.Architecture,
		a.Platform,
		a.Network,
		a.Topology,
	)
}

func ConvertToHistoricalData[D *AlertHistoricalDataRow | *DisruptionHistoricalDataRow](data []D) []HistoricalData {
	historicalData := make([]HistoricalData, len(data))
	for i, v := range data {
		historicalData[i] = HistoricalData(v)
	}
	return historicalData
}
