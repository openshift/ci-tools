# cluster-display

Web service for displaying information about OpenShift CI clusters.

## Overview

Cluster-display provides a web interface to view information about clusters used in OpenShift CI, including cluster pools, cluster deployments, and their status.

## Features

- View cluster pool information
- Display cluster deployment status
- Filter and search clusters
- Real-time cluster status updates

## Usage

```bash
cluster-display \
  --kubeconfig=/path/to/kubeconfig \
  --port=8090
```

## Options

- `--kubeconfig`: Path to kubeconfig file
- `--port`: Port to run the server on (default: 8090)
- `--log-level`: Logging level (default: info)
- `--gracePeriod`: Grace period for server shutdown

## Web Interface

The tool provides a web interface accessible at the configured port showing:
- Cluster pools and their status
- Cluster deployments
- Cluster availability
- Resource information

## Related

- [pkg/api](../../pkg/api) - Cluster profile and API definitions

