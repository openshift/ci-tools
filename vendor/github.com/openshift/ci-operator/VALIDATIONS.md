# CI Operator Validation Reference

CI Operator will validate the configuration file with the following rules.


## General
* both `tests` and `images` cannot be empty.
* `rpm_build_location` has to be defined with `rpm_build_commands`.
* `base_rpm_images` has to be defined with `rpm_build_commands`.
* `resources` cannot be empty, at least the blanket `*` has to be specified.

## More
### `build_root`
* One of the **image_stream_tag** or **project_image** has to be defined.


### `tests`
* `.as` should not be called 'images' because it gets confused with '[images]' target
* `.as` value has to be in [a-zA-Z0-9_.-] format.
* `.commands` value is **required**.

### `base_images` and `base_rpm_images`
* `.<name>` can't be named as `root`. This tag is already being used in `build_root`
* `.cluster` is optional. If it is defined, it has to be url.
* `.tag:` value is **required**.


### `tag_specification`
* `.cluster` value is **required**.
* `.namespace` value is **required**.
* `.tag` or `.name` values are **required**.

### `promotion`
* if `.namespace` is empty, `tag_specification.namespace` will be used.
* if `.tag` or `.name` are empty, `tag_specification.tag` or `tag_specification.name` will be used.

