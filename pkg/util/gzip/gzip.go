package gzip

import (
	"bytes"
	"compress/gzip"
	"io/ioutil"
)

// ReadFileMaybeGZIP wraps util.ReadFileMaybeGZIP, returning the decompressed contents
// if the file is gzipped, or otherwise the raw contents
func ReadFileMaybeGZIP(path string) ([]byte, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// check if file contains gzip header: http://www.zlib.org/rfc-gzip.html
	if !bytes.HasPrefix(b, []byte("\x1F\x8B")) {
		// go ahead and return the contents if not gzipped
		return b, nil
	}
	// otherwise decode
	gzipReader, err := gzip.NewReader(bytes.NewBuffer(b))
	if err != nil {
		return nil, err
	}
	return ioutil.ReadAll(gzipReader)
}
