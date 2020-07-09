# Job runtime analyzer

A cli that will fetch a `pods.json` artifact from a job run and print some statistics. Looks like this:

```
All runtimes
+-----------------+-----------------------+---------+
|       POD       |       CONTAINER       | RUNTIME |
+-----------------+-----------------------+---------+
| validate-vendor | artifacts             | 0s      |
| src-build       | manage-dockerfile     | 1s      |
| src-build       | extract-image-content | 2s      |
| validate-vendor | test                  | 15s     |
| src-build       | docker-build          | 1m26s   |
+-----------------+-----------------------+---------+
Runtimes by container
+-----------------------+--------+-------+-------+-------+------------+
|       CONTAINER       | MEDIAN |  MIN  |  MAX  | STDEV | SAMPLESIZE |
+-----------------------+--------+-------+-------+-------+------------+
| artifacts             | 0s     | 0s    | 0s    | 0s    |          1 |
| manage-dockerfile     | 1s     | 1s    | 1s    | 0s    |          1 |
| extract-image-content | 2s     | 2s    | 2s    | 0s    |          1 |
| test                  | 15s    | 15s   | 15s   | 0s    |          1 |
| docker-build          | 1m26s  | 1m26s | 1m26s | 0s    |          1 |
+-----------------------+--------+-------+-------+-------+------------+
```
