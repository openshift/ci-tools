/*
Package imagestreamtagwrapper implements a wrapper for a ctrlruntimeclient that assembles
imagestreamtags client-side by fetching the corresponding imagestream and image(s).

It is a workaround to get a caching client for imagestreamtags even though they do not support
the watch verb (xref https://github.com/openshift/api/issues/601).

Note that this still does not allow to get informers for imagestreamtags. Reacting to iamgestreamtags
can be achieved by reacting to imagestreams and then enqueue all referenced tags.
*/
package imagestreamtagwrapper
