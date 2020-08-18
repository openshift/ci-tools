# CI-Secret-Generator

This tool aims to automate the process of deployment of secrets to bitwarden while also providing a platform to document the commands used to generate these secrets. 

## Args and config.yaml

The tool expects a configuration like the one below which specifies the mapping between the itemName+attributeName/attachmentName/fieldName and the command used to
generate the secret.
The output of the command is stored into bitwarden as the contents of the field/attachment/password.

`password` is the only valid name which is accepted for an attribute

```yaml
- item_name: first_item
  field:
    name: field1
    cmd: echo -n secret
  attribute:
    name: password
    cmd: echo -n new_password
- item_name: second_item
  field:
    name: field2
    cmd: echo -n field2_contents
```

The above configuration tells the tool to use the following data to
create two bitwarden entries - 'first_item' and 'second_item'

* `field1` of `first_item` would be `secret`, and the `password` of `first_item` would be `new_password` in Bitwarden with item-name `first_item`,

* `field2` of `second_item`, would be `field2_contents` in Bitwarden with item-name `second_item`

## Run

```bash
$ echo -n "bw_password" > /tmp/bw_password 

$ ci-secret-generator --bw-password-path=/tmp/bw_password -bw-user kerberos_id@redhat.com --config <path_to_config.yaml>

```
