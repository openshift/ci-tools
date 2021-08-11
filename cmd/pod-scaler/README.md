# pod-scaler

The `pod-scaler` component automatically applies resource requests and limits to containers in batch workloads. Data is fetched from Prometheus, aggregated by using Pod labels stored in the the `kube_pod_labels` metric, and served in a mutating admission webhook for future workloads.

## Producer

The producer reads Prometheus data a couple times daily and updates a static data store in GCS after digesting the metrics. The storage format records time periods for which data fetching failed, to enable eventually consistent data collection in the face of Prometheus errors or network outages.

The overall size of the raw data, however, quickly grows unmanageable. In order to operate efficiently on this dataset we store compressed histograms for each execution trace. This allows us to reduce the data footprint while continuing to allow for dataset merging and aggregation. The <a href="https://www.circonus.com/2018/11/the-problem-with-percentiles-aggregation-brings-aggravation/">Circonus log-linear histogram</a> is used as it's performant, accurate, efficient and open-source.

## Consumers

### Admission

The admission controller is what actually implements the auto-scaling process by mutating all incoming Pods to ensure their containers have appropriate resource requests and limits. In order to provide an estimate of resource usage for containers in a CI job, this server analyzes metrics from previous executions of similar containers. Aggregate statistics are used to provide resource request recommendations by digesting prior metrics. It is assumed that, for a sufficiently similar container, resource usage will not vary much across executions - we expect this to be true for e.g. all executions of unit tests for some branch on a repository. This assumption allows for samples from all executions to be treated as one dataset with a single underlying distribution, so that aggregation can be done on the larger dataset to yield higher-fidelity signal.

The controller will not reduce a resource request or limit that already exists on a container, allowing users to override historical data. As our data is updated at most a couple times daily, this component can download the data once at startup, digest it and hold onto only the bare minimum necessary to serve requests and limits, allowing the server to have a very small footprint.

### UI

The UI is a React/PatternFly based web-app that serves all the historical data in the GCS data store and the resulting suggested resource requests. The UI uses histogram heatmaps to visualize the data, presenting distributions of resource usage for all executions of the CI container that have been indexed. Each vertical slice is a histogram, so a block represents the amount of time (number of samples) that the specific execution of the CI container spent using that much of the resource. Colors represent relative density - the yellower a block, the higher the corresponding bar in the histogram would be. The left-most vertical slice is the aggregate distribution, which contains all the data presented and is used to calculate the resource request recommendation. Note that the histograms used for storing distributions use an adaptive bucket size which varies with the logarithm of the values stored. As a result, the Y axis in the heatmaps are logarithmic, not linear, or smaller buckets would be almost invisible.

## Development

The root `Makefile` contains a number of easy targets to develop the `pod-scaler`. The underlying libraries that make local execution and development possible are used for the end-to-end tests, as well.

For instance, to start a Prometheus server locally, generate fake data for it, ingest the data, and start the UI and admission controllers, run:

```shell
make local-pod-scaler
```

In order to download the production dataset (warning: this is ~300MiB) and serve the UI in a development mode using `npm` (enabling hot-reload, etc), run:

```shell
make local-pod-scaler-ui
```

Run end-to-end tests as normal:

```shell
make local-e2e TESTFLAGS='-run TestAdmission' 
```