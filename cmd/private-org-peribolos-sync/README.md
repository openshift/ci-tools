# Private org peribolos sync

This tool generates a mapping table of peribolos repository configurations for the given private
organization. 
It walks through the release repository path, given by `--release-repo-path`, and detects which of the repositories
are promoting official images. The repositories that are specified in `--include-repo`, will be included if they exist in the release repository's path as well. Furthermore, it will get the required information for each of them from GitHub
and will generate its peribolos configuration.
Finally, the tool will update the peribolos configuration given by `--peribolos-config`.
