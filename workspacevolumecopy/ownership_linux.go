//go:build linux

package workspacevolumecopy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func inferVolumeIdentity(root string) (*uint32, *uint32, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, nil, fmt.Errorf("stat volume root: %w", err)
	}
	rootUID, rootGID, err := fileOwnerIDs(info)
	if err != nil {
		return nil, nil, err
	}

	var uid *uint32
	var gid *uint32
	if rootUID != 0 {
		value := rootUID
		uid = &value
	}
	if rootGID != 0 {
		value := rootGID
		gid = &value
	}
	if uid != nil && gid != nil {
		return uid, gid, nil
	}

	const sampleLimit = 8192
	uidCounts := map[uint32]int{}
	gidCounts := map[uint32]int{}
	uidSamples := 0
	gidSamples := 0
	entries := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		entryUID, entryGID, err := fileOwnerIDs(info)
		if err != nil {
			return err
		}
		if uid == nil && entryUID != 0 {
			uidCounts[entryUID]++
			uidSamples++
		}
		if gid == nil && entryGID != 0 {
			gidCounts[entryGID]++
			gidSamples++
		}
		entries++
		if entries >= sampleLimit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("inspect volume ownership: %w", err)
	}
	if uid == nil {
		uid, err = dominantIdentity(uidCounts, uidSamples, "UID")
		if err != nil {
			return nil, nil, err
		}
	}
	if gid == nil {
		gid, err = dominantIdentity(gidCounts, gidSamples, "GID")
		if err != nil {
			return nil, nil, err
		}
	}
	return uid, gid, nil
}

func dominantIdentity(counts map[uint32]int, samples int, kind string) (*uint32, error) {
	if samples == 0 {
		return nil, nil
	}
	var bestID uint32
	bestCount := 0
	for id, count := range counts {
		if count > bestCount {
			bestID = id
			bestCount = count
		}
	}
	// A clear majority lets ordinary workspace-owned data dominate occasional
	// service-owned files without silently guessing on genuinely mixed volumes.
	if bestCount*100 < samples*80 {
		return nil, fmt.Errorf("automatic workspace %s detection is ambiguous; set owner-%s override for this volume", kind, map[string]string{"UID": "uid", "GID": "gid"}[kind])
	}
	value := bestID
	return &value, nil
}

func fileOwnerIDs(info os.FileInfo) (uint32, uint32, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("filesystem ownership metadata is unavailable")
	}
	return stat.Uid, stat.Gid, nil
}
