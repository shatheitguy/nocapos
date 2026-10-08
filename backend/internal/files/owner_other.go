//go:build windows

package files

import "io/fs"

func owners(fs.FileInfo) (string, string) { return "", "" }
