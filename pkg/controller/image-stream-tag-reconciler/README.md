# Imagestreamtag reconciler

This controller is responsible for making sure that ImageStreamTags for the latest revision that was merged
exist, even if the publishing job fails for whatever reason.

To do so it:
* Watches Images (As ImageStreamTags do not support watching)
* Finds the corresponding promotion job or returns
* Checks if the ImageStreamTag was build from the latest revision in the given repo+branch
* If not: Enqueues a request onto the `prowjobreconciler`
* The `prowjobreconciler` then checks if there is currently an active prowjob for this revision and if not, creates one.

The two reconciler approach was chosen because in most cases, we build many ImageStreamTags from one ProwJob but we need to
react to ImageStreamTags. Using this approach allows us to de-duplicate requests for the same ProwJob and hence to avoid
creating one per ImageStreamTag it promotes to.
