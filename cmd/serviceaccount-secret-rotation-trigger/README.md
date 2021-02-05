# serviceaccount-secret-rotation-trigger

A small tool that will take a list of namespaces and:
* Add a TTL annotation to all serviceaccount secrets in them with a value of now + 24h: This will trigger the `serviceaccount_secret_refresher` controller to delete them as soon as that TTL is in the past
* Will update all ServiceAccounts in those namespaces to have empty Secrets and ImagePullSecrets fields: This will trigger an immediate recreation of those secrets by the corresponding minters
