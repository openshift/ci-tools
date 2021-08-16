# `lifecycle-oracle`

The release lifecycle oracle serves data about phases for versions of products. Currently one API is supported: `/api/phases/`. This will return a document of all phases for all versions for all products, e.g.:

```yaml
ocp: # a product
  "4.7": # a version
    previous: # all previous phases
      - event: "open" # the event
        when: "2020-05-17T14:47:58Z" # when it occurred, optional
    next: # the next event
      event: "feature-freeze"
  "4.6":
    previous:
    - event: "generally-available"
      when: "2020-04-17T14:47:58Z"
    - event: "code-freeze"
      when: "2020-03-17T14:47:58Z"
    - event: "feature-freeze"
      when: "2020-02-17T14:47:58Z"
    - event: "open"
      when: "2020-01-17T14:47:58Z"
    next:
      event: "end-of-life"
```