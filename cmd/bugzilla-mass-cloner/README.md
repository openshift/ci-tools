# Bugzilla Mass Cloner

This tool can be used to mass clone bugs from a target release to a desired one.

````sh
Usage of ./bugzilla-mass-cloner:
  -bugzilla-api-key-path string
    	Path to the file containing the Bugzilla API key.
  -bugzilla-endpoint string
    	Bugzilla's API endpoint.
  -bugzilla-github-external-tracker-id uint
    	The ext_type_id for GitHub external bugs, optional.
  -dry-run
    	Dry run for testing. Uses API tokens but does not mutate. (default true)
  -from-release string
    	From which targeted release the bug will be cloned to
  -to-release string
    	To which release value the cloned bugs will hold
```