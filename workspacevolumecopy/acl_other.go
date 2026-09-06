//go:build !linux

package workspacevolumecopy

import "context"

func remapTransferredACLs(context.Context, VolumePlan, []string) error {
	return nil
}
