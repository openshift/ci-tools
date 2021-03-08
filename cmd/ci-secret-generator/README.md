# CI-Secret-Generator

This tool aims to automate the process of deployment of secrets to Bitwarden while also providing a platform to document the commands used to generate these secrets. 

## Args and config.yaml

The tool expects a configuration like the one below which specifies the mapping between the `itemName`+`attributeName`/`attachmentName`/`fieldName` and the command used to generate the secret.
The output of the command is stored into Bitwarden as the contents of the field/attachment/password.

`password` is the only valid name which is accepted for an attribute

```yaml
- item_name: first_item
  fields:
    - name: field1
      cmd: echo -n secret
  attribute:
    name: password
    cmd: echo -n new_password
- item_name: second_item
  fields:
    name: field2
    cmd: echo -n field2_contents
```

The above configuration tells the tool to use the following data to
create two Bitwarden entries - 'first_item' and 'second_item'

* `field1` of `first_item` would be `secret`, and the `password` of `first_item` would be `new_password` in Bitwarden with item-name `first_item`,

* `field2` of `second_item`, would be `field2_contents` in Bitwarden with item-name `second_item`

Parameters can be passed in to decrease repetition in the configuration file by adding the `params` dictionary in the configuration file.  E.g.:

```yaml
- item_name: item$(cluster)$(env)
  fields:
    - name: field1
      cmd: echo "$(cluster) $(env)"
  params:
    cluster:
      - build01
      - build02
    env:
      - prod
      - staging
```
This would create four items with item names `itembuild01prod`, `itembuild02prod`, `itembuild01staging`, and `itembuild02staging`, and the corresponding `field1` which would contain the output of the corresponding `echo`, where the `$(paramname)` would be replaced with the values of the corresponding `paramname`.

## Run

```bash
$ echo -n "bw_password" > /tmp/bw_password

$ ci-secret-generator --bw-password-path=/tmp/bw_password --bw-user kerberos_id@redhat.com --config <path_to_config.yaml>

```
