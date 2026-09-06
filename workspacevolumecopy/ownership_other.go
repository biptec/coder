//go:build !linux

package workspacevolumecopy

import "errors"

func inferVolumeIdentity(string) (*uint32, *uint32, error) {
	return nil, nil, errors.New("automatic workspace UID/GID detection is only supported on Linux; set owner-uid and owner-gid overrides")
}
