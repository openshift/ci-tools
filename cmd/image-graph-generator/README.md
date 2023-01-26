# Image Graph Generator

This tool is responsible for reading multiple ci-operator configurations and generating a graph based on the connections
of all the organizations, repositories, branches, and images that are specified in each configuration.

The `image-graph-generator` is expected to operate against a [Dgraph](https://dgraph.io/) database.

The schema is defined and maintained in the `types.graphql` file.

Update or change or update the schema in the database:
```console
curl -X POST http://localhost:8080/admin/schema --data-binary '@types.graphql'
```


## Usage

```
Usage of image-graph-generator:
  -ci-operator-configs-path string
      Path to ci-operator configurations.
  -graphql-endpoint-address string
      Address of the Dgraph's graphql endpoint.
  -image-mirroring-path string
      Path to image mirroring mapping files.
```