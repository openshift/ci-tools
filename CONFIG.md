# CI Operator configuration file

### test_base_image

Base image for binary builds and following tests. Needs to have all build-time
dependencies for the tested repository.

#### Example:
```
"test_base_image": {
  "cluster": "https://api.ci.openshift.org",
  "namespace": "openshift",
  "name": "release",
  "tag": "golang-1.10"
}
```

TBD
