//go:build linux

package workspacevolumecopy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const aclBatchSize = 256

func remapTransferredACLs(ctx context.Context, volume VolumePlan, relativePaths []string) error {
	mapUID := volume.SourceOwnerUID != nil && volume.DestinationOwnerUID != nil &&
		*volume.SourceOwnerUID != 0 && *volume.SourceOwnerUID != *volume.DestinationOwnerUID
	mapGID := volume.SourceOwnerGID != nil && volume.DestinationOwnerGID != nil &&
		*volume.SourceOwnerGID != 0 && *volume.SourceOwnerGID != *volume.DestinationOwnerGID
	if (!mapUID && !mapGID) || len(relativePaths) == 0 {
		return nil
	}

	root := filepath.Clean(volume.Destination)
	paths := make([]string, 0, len(relativePaths))
	for _, relativePath := range relativePaths {
		candidate := root
		if relativePath != "" {
			candidate = filepath.Join(root, filepath.FromSlash(relativePath))
		}
		rel, err := filepath.Rel(root, candidate)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("transferred path %q escapes destination root", relativePath)
		}
		info, err := os.Lstat(candidate)
		if err != nil {
			return fmt.Errorf("stat transferred path %q: %w", relativePath, err)
		}
		// Linux symlinks do not carry access ACLs of their own. Avoid following a
		// destination symlink while applying ACL metadata.
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		paths = append(paths, candidate)
	}

	for start := 0; start < len(paths); start += aclBatchSize {
		end := min(start+aclBatchSize, len(paths))
		if err := remapACLBatch(ctx, paths[start:end], volume); err != nil {
			return err
		}
	}
	return nil
}

func remapACLBatch(ctx context.Context, paths []string, volume VolumePlan) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"-n", "-p"}, paths...)
	cmd := exec.CommandContext(ctx, "getfacl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("getfacl: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	transformed, changed, err := remapACLText(string(output), volume)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	cmd = exec.CommandContext(ctx, "setfacl", "--restore=-")
	cmd.Stdin = strings.NewReader(transformed)
	stderr.Reset()
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("setfacl --restore: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func remapACLText(input string, volume VolumePlan) (string, bool, error) {
	blocks := strings.Split(input, "\n\n")
	changed := false
	for blockIndex, block := range blocks {
		lines := strings.Split(block, "\n")
		if err := remapACLPrincipal(lines, "user", volume.SourceOwnerUID, volume.DestinationOwnerUID); err != nil {
			return "", false, err
		}
		if err := remapACLPrincipal(lines, "group", volume.SourceOwnerGID, volume.DestinationOwnerGID); err != nil {
			return "", false, err
		}
		newBlock := strings.Join(lines, "\n")
		if newBlock != block {
			changed = true
		}
		blocks[blockIndex] = newBlock
	}
	return strings.Join(blocks, "\n\n"), changed, nil
}

func remapACLPrincipal(lines []string, principal string, sourceID, destinationID *uint32) error {
	if sourceID == nil || destinationID == nil || *sourceID == 0 || *sourceID == *destinationID {
		return nil
	}
	source := strconv.FormatUint(uint64(*sourceID), 10)
	destination := strconv.FormatUint(uint64(*destinationID), 10)
	for _, scope := range []string{"", "default:"} {
		from := scope + principal + ":" + source + ":"
		to := scope + principal + ":" + destination + ":"
		hasSource := false
		hasDestination := false
		for _, line := range lines {
			hasSource = hasSource || strings.HasPrefix(line, from)
			hasDestination = hasDestination || strings.HasPrefix(line, to)
		}
		if hasSource && hasDestination {
			return fmt.Errorf("ACL %s mapping %s->%s collides with an existing destination principal; set an explicit ownership layout or remove the conflicting ACL", principal, source, destination)
		}
		if hasSource {
			for i, line := range lines {
				if strings.HasPrefix(line, from) {
					lines[i] = to + strings.TrimPrefix(line, from)
				}
			}
		}
	}
	return nil
}
