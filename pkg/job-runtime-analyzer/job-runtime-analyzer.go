package jobruntimeanalyzer

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kataras/tablewriter"
	"github.com/montanaflynn/stats"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	buildv1 "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

type podContainerRuntime struct {
	podName       string
	containerName string
	finished      bool
	duration      time.Duration
}

func podJSONURL(baseJobURL string) string {
	return strings.Join([]string{baseJobURL, "artifacts/build-resources/pods.json"}, "/")
}

func Run(baseJobURL string) error {
	stepGraphRaw, err := fetchFromURL(api.StepGraphJSONURL(baseJobURL))
	if err != nil {
		return fmt.Errorf("failed to fetch step graph json document: %w", err)
	}
	stepGraph := api.CIOperatorStepGraph{}
	if err := json.Unmarshal(stepGraphRaw, &stepGraph); err != nil {
		return fmt.Errorf("failed to unmarshal step graph: %w", err)
	}
	rawPodList, err := fetchFromURL(podJSONURL(baseJobURL))
	if err != nil {
		return fmt.Errorf("failed to fetch pod json: %w", err)
	}

	podList := corev1.PodList{}
	if err := json.Unmarshal(rawPodList, &podList); err != nil {
		return fmt.Errorf("failed to unmarshal pod list: %w", err)
	}
	podList = filterPods(podList, stepGraph)

	var runtimes []*podContainerRuntime
	for _, pod := range podList.Items {
		for _, container := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			item := &podContainerRuntime{
				podName:       pod.Name,
				containerName: container.Name,
			}
			runtimes = append(runtimes, item)
			if container.State.Terminated == nil {
				continue
			}
			item.finished = true
			item.duration = container.State.Terminated.FinishedAt.Sub(container.State.Terminated.StartedAt.Time)
		}
	}
	sort.Slice(runtimes, func(i, j int) bool {
		return runtimes[i].duration < runtimes[j].duration
	})

	runtimesByContainer, err := runtimesByContainer(runtimes)
	if err != nil {
		return fmt.Errorf("failed to calculate runtimes by container: %w", err)
	}

	printRuntimes("All runtimes", runtimes)
	printRuntimeByContainer(runtimesByContainer)

	return nil
}

func printRuntimes(title string, data []*podContainerRuntime, footers ...[]string) {
	_, _ = fmt.Printf("%s\n", title)
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"pod", "container", "runtime"})
	for _, item := range data {
		table.Append([]string{item.podName, item.containerName, item.duration.String()})
	}
	for _, footer := range footers {
		table.SetFooter(footer)
	}
	table.Render()
}

type containerSummary struct {
	name    string
	median  float64
	max     float64
	min     float64
	stdev   float64
	samples int
}

func runtimesByContainer(data []*podContainerRuntime) ([]containerSummary, error) {
	containerRuntimeMap := map[string][]float64{}
	for _, entry := range data {
		containerRuntimeMap[entry.containerName] = append(containerRuntimeMap[entry.containerName], float64(entry.duration))
	}

	var results []containerSummary
	var errs []error
	for containerName, containerDurationSlice := range containerRuntimeMap {
		result := containerSummary{name: containerName, samples: len(containerDurationSlice)}
		median, err := stats.Median(stats.Float64Data(containerDurationSlice))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to calculate median for %s: %w", containerName, err))
			continue
		}
		result.median = median
		max, err := stats.Max(stats.Float64Data(containerDurationSlice))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to calculate max for %s: %w", containerName, err))
			continue
		}
		result.max = max
		min, err := stats.Min(stats.Float64Data(containerDurationSlice))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to calculate min for %s: %w", containerName, err))
			continue
		}
		result.min = min
		stddev, err := stats.StandardDeviation(stats.Float64Data(containerDurationSlice))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to calculate standard deviation for %s: %w", containerName, err))
			continue
		}
		result.stdev = stddev
		results = append(results, result)
	}

	return results, utilerrors.NewAggregate(errs)
}

func printRuntimeByContainer(data []containerSummary) {
	sort.Slice(data, func(i, j int) bool {
		return data[i].max < data[j].max
	})

	_, _ = fmt.Printf("Runtimes by container\n")
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"container", "median", "min", "max", "stdev", "samplesize"})
	for _, item := range data {
		table.Append([]string{item.name, time.Duration(item.median).String(), time.Duration(item.min).String(), time.Duration(item.max).String(), time.Duration(item.stdev).String(), strconv.Itoa(item.samples)})
	}
	table.Render()
}

func fetchFromURL(urlString string) ([]byte, error) {
	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s as url: %w", urlString, err)
	}

	result, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to GET %s: %w", parsedURL.String(), err)
	}
	defer result.Body.Close()
	body, err := ioutil.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body for request to %s: %w", parsedURL.String(), err)
	}
	if result.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got unexpected http status code %d for url %s. Response body:\n%s", result.StatusCode, parsedURL.String(), string(body))
	}

	return body, nil
}

func filterPods(pods corev1.PodList, steps api.CIOperatorStepGraph) corev1.PodList {
	result := corev1.PodList{}
	for _, pod := range pods.Items {
		for _, step := range steps {
			if pod.Name != step.StepName && pod.Labels[buildv1.BuildLabel] != step.StepName {
				continue
			}
			result.Items = append(result.Items, pod)
		}
	}
	return result
}
