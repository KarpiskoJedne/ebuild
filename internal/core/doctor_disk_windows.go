//go:build windows

package core

import "errors"

func diskFree(path string) (int64, error) {
	return 0, errors.New("disk probe not implemented on windows")
}
