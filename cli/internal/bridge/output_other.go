//go:build !unix

package bridge

import "os"

// OutputClosed tells if the read end of the output f is closed. This OS has no such
// examination, thus it gives false. After the end of the standard input, the bridge then
// waits a maximum of drainTimeout for the answers of the requests in flight.
func OutputClosed(*os.File) bool { return false }
