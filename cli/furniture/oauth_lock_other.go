//go:build !unix

package furniture

// lockFile does not lock on this platform: processes sharing a token file
// may refresh concurrently.
func lockFile(path string) (func(), error) {
	return func() {}, nil
}
