//go:build windows

package appdata

import "io/fs"

func ownerOf(fs.FileInfo) string     { return "" }
func chownLike(string, fs.FileInfo) {}
