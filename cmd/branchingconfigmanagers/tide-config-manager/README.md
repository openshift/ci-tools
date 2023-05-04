Tool is used to change merge criteria during around branching and release events.

Mandatory parameters:

```
/tide-config-manager --lifecycle-phase branching --prow-config-dir /dir --sharded-prow-config-base-dir /dir --current-release 4.x
```

Lifecycle phase can be: `branching`, `pre-general-availability` or `general-availability`.
