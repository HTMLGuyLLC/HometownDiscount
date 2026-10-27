//go:build !unix

package slot

import "os"

// Windows runs the map and hints only, never the local model, so there's
// nothing to take turns on.
func tryLock(*os.File) (bool, error) { return true, nil }

func unlock(*os.File) {}
