The job-run-aggregator finds multiple runs of the same job for the same payload and analyzes the overall result
and the individual junit results.

The analysis allows failures within (ideally) a standard deviation of the norm for individual tests.
This will allow a single payload to checked by multiple parallel job runs and the average results for each test
checked to ensure that a regression hasn't happened.
That property allows us to have less than perfect test results to start and still be able to latch improvements into
the failure percentages, giving a path to improvement.