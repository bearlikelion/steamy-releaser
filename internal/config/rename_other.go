//go:build !linux && !darwin && !windows

package config

import "errors"

func renameConfigurationNoReplace(_, _ string) error {
	return errors.New("exclusive configuration rename is not supported on this platform")
}
