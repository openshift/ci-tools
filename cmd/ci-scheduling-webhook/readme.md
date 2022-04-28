# Manual Image Builds 
```shell
[ci-tools]$ CGO_ENABLED=0 go build -ldflags="-extldflags=-static" github.com/openshift/ci-tools/cmd/ci-scheduling-webhook
[ci-tools]$ sudo docker build -t quay.io/jupierce/ci-scheduling-webhook:latest -f images/ci-scheduling-webhook/Dockerfile .
# Temporary hosting location
[ci-tools]$ sudo docker push quay.io/jupierce/ci-scheduling-webhook:latest
```

# Local Test
```shell
[ci-tools]$ export KUBECONFIG=~/.kube/config
[ci-tools]$ go run github.com/openshift/ci-tools/cmd/ci-scheduling-webhook --as system:admin --port 8443 --shrink-cpu-requests-tests 0.3 &
[ci-tools]$ cmd/ci-scheduling-webhook/testing/post.sh
```

# Manual Deployment
```shell
[ci-tools]$ oc --as system:admin apply -f ./cmd/ci-scheduling-webhook/res/admin.yaml
[ci-tools]$ oc --as system:admin apply -f ./cmd/ci-scheduling-webhook/res/deployment.yaml
# Check deployment is running
[ci-tools]$ oc --as system:admin apply -f ./cmd/ci-scheduling-webhook/res/webhook.yaml
```
