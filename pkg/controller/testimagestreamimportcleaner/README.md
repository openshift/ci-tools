# testimagestreamimportcleaner

A simplistic controller that deletes all testimagestreamimport custom resources
that are older than seven days. Jobs create these to request the `test_images_distributor`
to import an image. To avoid importing an image indefinitely as soon as it has been used
once, we need to clean them up.
