# repo-init

The `repo-init` component allows a user to on-board a new repository to the CI Test Platform.
## CLI

To run the tool in CLI mode, execute

```shell
repo-init --mode=cli --release-repo=/path/to/release/repo
```

### API

The API is used by the UI component to authenticate against GitHub, validate configurations, generate configurations, and also to generate pull requests against the `release` repository for new configurations.
To start up the API you may run something like

```shell
repo-init --mode=api --port=8080 --github-token-path=/tmp/token --github-endpoint=https://api.github.com --num-repos=4 --server-config-path=/tmp/serverconfig
```

where `/tmp/serverconfig` would contain these files:

`github-client-id` - this file should contain the client ID of the OAuth application in GitHub.

`github-client-secret` - this file should contain the client secret of the OAuth application in GitHub.

`github-redirect-uri`  - this file should contain the redirect URI of the OAuth application in GitHub.

### UI

The UI is a React/PatternFly based web-app that presents the on-boarding flow as a Wizard component. Here, the user may enter details about the component, such as build information (is it an optional operator build, what tests need to be executed, etc.). At the end of the workflow,
a ci-operator config will be generated. The user may at this point choose to simply push that config to their own release repo, or also to create a pull request for the upstream release repo.

To run the UI locally you may run something like:

```shell
npm run start:dev
```

from within the `frontend` dir. This will start the UI in development mode where any changes you make will be hot-reloaded.

Additionally, you may execute the built UI like so:

```shell
repo-init --mode=ui --port=9000 --metrics-port=9001 --health-port=9002
```

Note that you must first have built the UI by executing:

```shell
npm run build
```

from within the `frontend` dir.

## Development

For local development you should have a `/cmd/repo-init/frontend/.env` file with something like this in it:

```
REACT_APP_API_URI=http://localhost:8080/api
```

The easiest way to run the API/UI locally is to execute the `/hack/local-repo-init-ui.sh` script, like this:

```shell
./local-repo-init-ui.sh start
```

or

```shell
./local-repo-init-ui.sh stop
```

to stop a running instance and clean up temporary files.

The root `Makefile` contains some convenience targets for deploying a test instance of the `repo-init` API and UI based on a current pull request.

```shell
make pr-deploy-repo-init-api
```

and

```shell
make pr-deploy-repo-init-ui
```

After this, you should have a working copy of the `repo-init` component deployed that you can test with.