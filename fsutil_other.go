//go:build !unix

package sysproxy

import "os"

func preserveOwner(*os.File, os.FileInfo) {}
