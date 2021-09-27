# LDAP users from GitHub owners

A small cli that extracts the ldap usernames from a directory hirarchy containing Owners files with GitHub usernames.

Requires a pre-existing file from an ldap query:
```
ldapsearch -LLL -x -h ldap.corp.redhat.com -b ou=users,dc=redhat,dc=com '(rhatSocialURL=GitHub*)' rhatSocialURL uid 2>&1|tee /tmp/out
```

Sample usage:
```
go run -race ./cmd/ldap-users-from-github-owners-files  -ldap-file /tmp/out --repo-base-dir ../release -repo-sub-dir core-services/secret
```

or generating the mapping files only:

```console
go run -race ./cmd/ldap-users-from-github-owners-files  -ldap-file /tmp/out -mapping-file /tmp/mapping.yaml
```