# github-ldap-user-group-creator

Given the mapping from github login to Red Had LDAP kerberos ID, this tool maintains a user group
for each user in the map on every cluster in our build farm.

For example, `m(some_name_at_github)=another_name_at_ldap` leads to a group

```yaml
apiVersion: user.openshift.io/v1
kind: Group
metadata:
  name: some_name_at_github-group
users:
  - some_name_at_github
  - another_name_at_ldap
```

The group `some_name_at_github-group` will be used by `ci-operator` which promotes the group to the admins of the namespace created for the test.
