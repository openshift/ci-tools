# CI-Secret-Generator

This tool automates the process of deployment of secrets while also providing a
platform to document the commands used to generate them.

## Target

Different output targets are supported, which can be specified with the
`--target` command-line option.

### `validate`

Verifies that the configuration is well-formed in respect to itself and the
[`ci-secret-bootstrap`](../ci-secret-bootstrap) configuration, which has to be
specified with the `--bootstrap-config` parameter.

### `file`

Executes the input commands and writes debug output to a local file.  A
temporary file is used unless the `--output-file` parameter is specified.

### `bitwarden`

Currently the secret back end used in production.  Secrets are stored in
field/attachment/password elements in Bitwarden depending on their size
requirements.

For attributes, `password` is the only valid name which is accepted.

## Arguments and `config.yaml`

The tool expects a configuration like the one below which specifies the mapping
between the `itemName`+`attributeName`/`attachmentName`/`fieldName` and the
command used to generate the content of the item.

The output of the command is stored into the secret back end as the contents of
the field/attachment/password.

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

The above configuration tells the tool to use the following data to create two
entries - 'first_item' and 'second_item'.

* `field1` of `first_item` will be `secret`, and the `password` of `first_item`
  will be `new_password` with item-name `first_item`,

* `field2` of `second_item` will be `field2_contents` with item-name
  `second_item`

Parameters can be passed in to decrease repetition in the configuration file by
adding a `params` dictionary to the entry:

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

This will create four items with item names `itembuild01prod`,
`itembuild02prod`, `itembuild01staging`, and `itembuild02staging`, and the
corresponding `field1` which will contain the output of the corresponding
`echo`, where the `$(paramname)` will be replaced with the values of the
corresponding `paramname`.

## Run

```bash
$ echo -n "bw_password" > /tmp/bw_password

$ ci-secret-generator --bw-password-path=/tmp/bw_password --bw-user kerberos_id@redhat.com --config <path_to_config.yaml>

```
