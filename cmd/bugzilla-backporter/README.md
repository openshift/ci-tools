# bugzilla-backporter

Web service for managing Bugzilla bug clones and backports.

## Overview

The bugzilla-backporter provides a web interface and API for creating and managing Bugzilla bug clones across different target releases. It's used to track and manage backports of bugs to different OpenShift versions.

## Features

- Create bug clones for different target releases
- View existing clones for a bug
- Get bug details in JSON format
- Landing page with usage information

## Usage

```bash
bugzilla-backporter \
  --bugzilla-api-key-path=/path/to/api-key \
  --plugin-config=/etc/plugins/plugins.yaml \
  --address=:8080
```

## Endpoints

- `GET /` - Landing page with help information
- `GET /clones` - List clones for a bug
- `POST /clones/create` - Create a new bug clone
- `GET /bug` - Get bug details in JSON format
- `GET /help` - Help information

## Configuration

Requires:
- Bugzilla API key
- Plugin configuration file (for target release versions)

## Related Packages

- [pkg/backporter](../../pkg/backporter) - Bugzilla backporting logic

