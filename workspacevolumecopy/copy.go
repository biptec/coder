package workspacevolumecopy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
)

const (
	rsyncOutputPrefix = "__CODER_VOLUME_COPY__"

	AnnotationPrefix        = "com.coder.volume-copy/"
	AnnotationLogicalKey    = AnnotationPrefix + "logical-key"
	AnnotationDisplayName   = AnnotationPrefix + "display-name"
	AnnotationMountPath     = AnnotationPrefix + "mount-path"
	AnnotationExcludedPaths = AnnotationPrefix + "excluded-paths"
	AnnotationOwnerUID      = AnnotationPrefix + "owner-uid"
	AnnotationOwnerGID      = AnnotationPrefix + "owner-gid"

	PlanEnv = "CODER_WORKSPACE_VOLUME_COPY_PLAN"
)

type Plan struct {
	// AllowSourceChanges means the source workspace may be running. rsync exit
	// code 24 (files vanished while walking the source) is then treated as a
	// successful best-effort live copy rather than a hard failure.
	AllowSourceChanges bool         `json:"allow_source_changes,omitempty"`
	Volumes            []VolumePlan `json:"volumes"`
}

type VolumePlan struct {
	Key                 string   `json:"key"`
	Source              string   `json:"source"`
	Destination         string   `json:"destination"`
	Overwrite           bool     `json:"overwrite"`
	ExcludedPaths       []string `json:"excluded_paths,omitempty"`
	SourceOwnerUID      *uint32  `json:"source_owner_uid,omitempty"`
	SourceOwnerGID      *uint32  `json:"source_owner_gid,omitempty"`
	DestinationOwnerUID *uint32  `json:"destination_owner_uid,omitempty"`
	DestinationOwnerGID *uint32  `json:"destination_owner_gid,omitempty"`
}

func RunFromEnvironment(ctx context.Context, stdout, stderr io.Writer) error {
	rawPlan := os.Getenv(PlanEnv)
	if strings.TrimSpace(rawPlan) == "" {
		return errors.New(PlanEnv + " is required")
	}
	var plan Plan
	if err := json.Unmarshal([]byte(rawPlan), &plan); err != nil {
		return fmt.Errorf("decode volume copy plan: %w", err)
	}
	return Copy(ctx, plan, stdout, stderr)
}

func Copy(ctx context.Context, plan Plan, stdout, stderr io.Writer) error {
	if len(plan.Volumes) == 0 {
		return errors.New("volume copy plan is empty")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	seen := make(map[string]struct{}, len(plan.Volumes))
	for _, volume := range plan.Volumes {
		if strings.TrimSpace(volume.Key) == "" {
			return errors.New("volume key is required")
		}
		if _, ok := seen[volume.Key]; ok {
			return fmt.Errorf("duplicate volume key %q", volume.Key)
		}
		seen[volume.Key] = struct{}{}

		volume, err := resolveVolumeOwnership(volume)
		if err != nil {
			return fmt.Errorf("volume %q: %w", volume.Key, err)
		}
		args, err := rsyncArgs(volume)
		if err != nil {
			return fmt.Errorf("volume %q: %w", volume.Key, err)
		}

		var outputCapture bytes.Buffer
		var errorOutput bytes.Buffer
		cmd := exec.CommandContext(ctx, "rsync", args...)
		cmd.Stdout = io.MultiWriter(stdout, &outputCapture)
		cmd.Stderr = io.MultiWriter(stderr, &errorOutput)
		rsyncErr := cmd.Run()
		liveChanged := false
		if rsyncErr != nil {
			var exitErr *exec.ExitError
			if plan.AllowSourceChanges && errors.As(rsyncErr, &exitErr) && exitErr.ExitCode() == 24 {
				liveChanged = true
			} else {
				message := strings.TrimSpace(errorOutput.String())
				if message != "" {
					return fmt.Errorf("rsync volume %q: %w: %s", volume.Key, rsyncErr, message)
				}
				return fmt.Errorf("rsync volume %q: %w", volume.Key, rsyncErr)
			}
		}
		transferred, err := parseRsyncTransferredPaths(outputCapture.String())
		if err != nil {
			return fmt.Errorf("volume %q: parse rsync transfer list: %w", volume.Key, err)
		}
		if err := remapTransferredACLs(ctx, volume, transferred); err != nil {
			return fmt.Errorf("volume %q: remap ACL ownership: %w", volume.Key, err)
		}
		if liveChanged {
			_, _ = fmt.Fprintf(stderr, "volume %q: source files changed during live copy; continuing after rsync exit code 24\n", volume.Key)
		}
	}
	return nil
}

func rsyncArgs(volume VolumePlan) ([]string, error) {
	source := path.Clean(strings.TrimSpace(volume.Source))
	destination := path.Clean(strings.TrimSpace(volume.Destination))
	if !path.IsAbs(source) || !path.IsAbs(destination) {
		return nil, errors.New("source and destination must be absolute paths")
	}
	if source == destination {
		return nil, errors.New("source and destination must differ")
	}

	excluded, err := normalizeExcludedPaths(volume.ExcludedPaths)
	if err != nil {
		return nil, err
	}

	// No --delete is intentionally present: destination-only data is always
	// preserved. --ignore-existing implements the per-volume Overwrite=false
	// contract while still allowing missing files inside existing directories to
	// be merged into the destination.
	args := []string{
		"-aHAX",
		"--numeric-ids",
		"--out-format=" + rsyncOutputPrefix + "%i|%n",
		"--no-devices",
		"--no-specials",
		"--human-readable",
		"--stats",
	}
	if !volume.Overwrite {
		args = append(args, "--ignore-existing")
	}
	if volume.SourceOwnerUID != nil && volume.DestinationOwnerUID != nil && *volume.SourceOwnerUID != 0 && *volume.SourceOwnerUID != *volume.DestinationOwnerUID {
		args = append(args, fmt.Sprintf("--usermap=%d:%d", *volume.SourceOwnerUID, *volume.DestinationOwnerUID))
	}
	if volume.SourceOwnerGID != nil && volume.DestinationOwnerGID != nil && *volume.SourceOwnerGID != 0 && *volume.SourceOwnerGID != *volume.DestinationOwnerGID {
		args = append(args, fmt.Sprintf("--groupmap=%d:%d", *volume.SourceOwnerGID, *volume.DestinationOwnerGID))
	}
	for _, excludedPath := range excluded {
		args = append(args, "--exclude=/"+excludedPath)
	}
	args = append(args, strings.TrimSuffix(source, "/")+"/", strings.TrimSuffix(destination, "/")+"/")
	return args, nil
}

func parseRsyncTransferredPaths(output string) ([]string, error) {
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, rsyncOutputPrefix) {
			continue
		}
		payload := strings.TrimPrefix(line, rsyncOutputPrefix)
		separator := strings.IndexByte(payload, '|')
		if separator < 0 {
			return nil, fmt.Errorf("invalid rsync itemized output %q", line)
		}
		rawPath := payload[separator+1:]
		decodedPath, err := decodeRsyncPath(rawPath)
		if err != nil {
			return nil, err
		}
		clean := path.Clean(strings.TrimSuffix(decodedPath, "/"))
		if clean == "." {
			clean = ""
		}
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("unsafe transferred path %q", decodedPath)
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}
	return paths, nil
}

func decodeRsyncPath(raw string) (string, error) {
	var decoded strings.Builder
	for i := 0; i < len(raw); {
		if i+5 <= len(raw) && raw[i] == '\\' && raw[i+1] == '#' {
			value := 0
			valid := true
			for j := i + 2; j < i+5; j++ {
				if raw[j] < '0' || raw[j] > '7' {
					valid = false
					break
				}
				value = value*8 + int(raw[j]-'0')
			}
			if valid {
				decoded.WriteByte(byte(value))
				i += 5
				continue
			}
		}
		decoded.WriteByte(raw[i])
		i++
	}
	return decoded.String(), nil
}

func resolveVolumeOwnership(volume VolumePlan) (VolumePlan, error) {
	sourceUID, sourceGID, err := resolveVolumeIdentity(volume.Source, volume.SourceOwnerUID, volume.SourceOwnerGID)
	if err != nil {
		return VolumePlan{}, fmt.Errorf("resolve source ownership: %w", err)
	}
	destinationUID, destinationGID, err := resolveVolumeIdentity(volume.Destination, volume.DestinationOwnerUID, volume.DestinationOwnerGID)
	if err != nil {
		return VolumePlan{}, fmt.Errorf("resolve destination ownership: %w", err)
	}
	if (sourceUID == nil) != (destinationUID == nil) {
		return VolumePlan{}, errors.New("workspace user UID could not be inferred on both source and destination; set owner-uid override on the ambiguous volume")
	}
	if (sourceGID == nil) != (destinationGID == nil) {
		return VolumePlan{}, errors.New("workspace user GID could not be inferred on both source and destination; set owner-gid override on the ambiguous volume")
	}
	volume.SourceOwnerUID = sourceUID
	volume.SourceOwnerGID = sourceGID
	volume.DestinationOwnerUID = destinationUID
	volume.DestinationOwnerGID = destinationGID
	return volume, nil
}

func resolveVolumeIdentity(root string, uidOverride, gidOverride *uint32) (*uint32, *uint32, error) {
	if uidOverride != nil && gidOverride != nil {
		return uidOverride, gidOverride, nil
	}
	uid, gid, err := inferVolumeIdentity(root)
	if err != nil {
		return nil, nil, err
	}
	if uidOverride != nil {
		uid = uidOverride
	}
	if gidOverride != nil {
		gid = gidOverride
	}
	return uid, gid, nil
}

func normalizeExcludedPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, excludedPath := range paths {
		excludedPath = strings.TrimSpace(excludedPath)
		if excludedPath == "" {
			continue
		}
		if path.IsAbs(excludedPath) {
			return nil, fmt.Errorf("excluded path %q must be relative", excludedPath)
		}
		clean := path.Clean(excludedPath)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("invalid excluded path %q", excludedPath)
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out, nil
}
