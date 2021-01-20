package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gonum.org/v1/gonum/stat"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func main() {
	file := flag.String("logfile", "", "The logfile to read")
	startMsg := flag.String("start-msg", "Building tide pool.", "The first message that identifies the start of a messagegroup")
	endMsg := flag.String("end-msg", "Synced", "The message that indicates the end of a messagegroup")
	flag.Parse()

	data, err := ioutil.ReadFile(*file)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read file")
	}

	var result []LogrusEntry
	var errs []error
	for idx, line := range bytes.Split(data, []byte("\n")) {
		entry := &LogrusEntry{}
		if err := json.Unmarshal(line, entry); err != nil {
			errs = append(errs, fmt.Errorf("failed to unmarshal line %d: %w", idx, err))
			continue
		}
		result = append(result, *entry)
	}

	if err := utilerrors.NewAggregate(errs); err != nil {
		for _, err := range err.Errors() {
			logrus.WithError(err).Warning("Encountered error parsing line")
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Time.Before(result[j].Time)
	})

	var messagGroups []messageGroup
	logrus.Infof("found %d messages", len(result))

	var currentMessageGroup messageGroup
	for _, entry := range result {
		if entry.Message == *startMsg {
			logrus.Info("Found start")
			currentMessageGroup = messageGroup{}
		}
		currentMessageGroup.Messages = append(currentMessageGroup.Messages, entry)

		if entry.Message == *endMsg && len(currentMessageGroup.Messages) > 0 {
			logrus.Info("Found end")
			currentMessageGroup.Duration = currentMessageGroup.Messages[len(currentMessageGroup.Messages)-1].Time.Sub(currentMessageGroup.Messages[0].Time)
			messagGroups = append(messagGroups, currentMessageGroup)
		}
	}

	//	sort.Slice(messagGroups, func(i, j int) bool {
	//		return messagGroups[i].Duration < messagGroups[j].Duration
	//	})

	//	data, err = json.Marshal(messagGroups)
	//	if err != nil {
	//		logrus.WithError(err).Fatal("failed to marshal")
	//	}
	//	fmt.Println(string(data))
	//	_ = data

	timings := timings{}
	// We have to do two passes to filter out single-sample transitions. This helps
	// with getting rid of prinf log statemens.
	samples := map[string]int{}
	for _, group := range messagGroups {
		for idx, message := range group.Messages {
			if idx == 0 {
				continue
			}
			if message.Message == "" || group.Messages[idx-1].Message == "" {
				continue
			}
			samples[key(group.Messages[idx-1].Message, message.Message)]++
		}
	}

	for _, group := range messagGroups {
		timings.Insert("Overall", float64(group.Duration))
		for idx, message := range group.Messages {
			if idx == 0 {
				continue
			}
			if samples[key(group.Messages[idx-1].Message, message.Message)] < 10 {
				continue
			}

			timings.Insert(key(group.Messages[idx-1].Message, message.Message), float64(message.Time.Sub(group.Messages[idx-1].Time)))
		}
	}

	// Filter Max == 0
	for key, val := range timings {
		if val.Max == 0 {
			delete(timings, key)
			continue
		}
	}

	data, err = json.Marshal(timings)
	if err != nil {
		logrus.WithError(err).Fatal("failed to marshal result")
	}

	fmt.Fprintln(os.Stdout, string(data))
}

func key(old, current string) string {
	old = strings.ReplaceAll(old, "\n", "")
	current = strings.ReplaceAll(current, "\n", "")
	return fmt.Sprintf("'%s' -> '%s'", old, current)
}

type timings map[string]*Timing

func (t timings) Insert(Name string, value float64) {
	tmng, ok := t[Name]
	if !ok {
		t[Name] = &Timing{}
		tmng = t[Name]
	}

	if value < tmng.Min {
		tmng.Min = value
	}
	if value > tmng.Max {
		tmng.Max = value
	}
	tmng.Samples = append(tmng.Samples, value)
	tmng.Mean = stat.Mean(tmng.Samples, nil)
	tmng.StdDev = stat.StdDev(tmng.Samples, nil)
}

func (t timings) MarshalJSON() ([]byte, error) {
	type printedTiming struct {
		Name    string
		Samples int
		Min     string
		Max     string
		Mean    string
		StdDev  string
	}

	var toEncode []printedTiming
	for name, val := range t {
		toEncode = append(toEncode, printedTiming{
			Name:    name,
			Samples: len(val.Samples),
			Min:     prettyDuration(val.Min),
			Max:     prettyDuration(val.Max),
			Mean:    prettyDuration(val.Mean),
			StdDev:  prettyDuration(val.StdDev),
		})
	}

	return json.Marshal(toEncode)
}

func prettyDuration(in float64) string {
	return time.Duration(int64(in)).String()
}

type Timing struct {
	Samples []float64
	Min     float64
	Max     float64
	Mean    float64
	StdDev  float64
}

type messageGroup struct {
	Messages []LogrusEntry
	Duration time.Duration
}

func (mg messageGroup) MarshalJSON() ([]byte, error) {
	toMarshal := struct {
		Messages []LogrusEntry `json:"messages,omitempty"`
		Duration string        `json:"duration"`
	}{
		Messages: mg.Messages,
		Duration: mg.Duration.String(),
	}

	return json.Marshal(toMarshal)
}

type LogrusEntry logrus.Entry

func (le *LogrusEntry) UnmarshalJSON(d []byte) error {
	tmp := map[string]interface{}{}
	if err := json.Unmarshal(d, &tmp); err != nil {
		return fmt.Errorf("failed to unmarshal into map[string]interface{}: %w", err)
	}

	if val, ok := tmp["msg"]; ok {
		le.Message = val.(string)
		delete(tmp, "msg")
	}
	if val, ok := tmp["time"]; ok {
		timeVal, err := time.Parse(time.RFC3339, val.(string))
		if err != nil {
			return fmt.Errorf("failed to parse time: %w", err)
		}
		le.Time = timeVal
		delete(tmp, "time")
	}
	if val, ok := tmp["level"]; ok {
		level, err := logrus.ParseLevel(val.(string))
		if err != nil {
			return fmt.Errorf("failed to parse loglevel: %w", err)
		}
		le.Level = level
	}

	le.Data = tmp

	return nil
}
