# Private Prow Configs Mirror

The purpose of this tool is to generate the prow's configuration for the repositories of the `openshift-priv`
organization. 

It walks through all the ci-operator configuration files to get the information of which repositories are promoting
official images.
When a configuration is detected, the tool generates the configuration for the corresponding repository of the `openshift-priv` organization instead.

The following components of the configs will be affected:

### Prow Config
* branch-protection
* context_options
* tide.merge_method
* tide.queries
* tide.pr_status_base_urls
* plank.default_decoration_configs
* plank.job_url_prefix_config

### Prow Plugins
* approve
* lgtm
* plugins
* bugzilla
