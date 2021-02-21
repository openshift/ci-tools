# Vault Subpath Proxy

A small proxy that we run in front of Vault. It solves the problem of "If user has no list perm in parent directory,
user can't find any subdirectory they might have access to.". To do so it:
* Checks if a request got a 403
* If so, it will call out to the `/v1/sys/internal/ui/resultant-acl` endpoint, using the provided token
* That endpoint returns effective permissions, so if a user has access to a subpath, it is there
* If any result is found this way, it will be added to the response and the status code is changed from 403 to 200

Careful: The `resultant-acl` api is internal, undocumented and no stability guarantee is provided. Ideally, this
functionality will get included into Vault itself one day.
