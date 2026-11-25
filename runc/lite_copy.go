package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

const liteVolumeInitMarker = ".sysrunc-volume-init-incomplete"

func seedLitePVC(imagePath, destination string, linux *specs.Linux) error {
	fd, err := unix.Open(destination, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open PVC directory: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock PVC directory: %w", err)
	}
	defer unix.Flock(fd, unix.LOCK_UN)

	marker := filepath.Join(destination, liteVolumeInitMarker)
	retry, err := liteMarkerExists(marker)
	if err != nil {
		return err
	}
	empty, err := dirEmpty(destination)
	if err != nil {
		return err
	}
	if !empty && !retry {
		return nil
	}
	info, err := os.Lstat(imagePath)
	if errors.Is(err, os.ErrNotExist) {
		if retry {
			return os.Remove(marker)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("image PVC directory %q is not a real directory", imagePath)
	}
	if !retry {
		markerFD, err := unix.Open(marker, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if err != nil {
			return fmt.Errorf("create PVC initialization marker: %w", err)
		}
		if err := unix.Close(markerFD); err != nil {
			return err
		}
	}
	if err := copyLiteDir(imagePath, destination, linux); err != nil {
		return err // Leave the marker so the next start completes the copy.
	}
	return os.Remove(marker)
}

func liteMarkerExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return false, fmt.Errorf("unsafe PVC initialization marker %q", path)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return false, err
	}
	if stat.Uid != 0 {
		return false, fmt.Errorf("PVC initialization marker %q is not owned by root", path)
	}
	return true, nil
}

// Reject a symlink at any component of an image path before rsync walks it.
func liteImageDir(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("image path %q escapes rootfs", relative)
	}
	path := root
	for _, component := range append([]string{""}, strings.Split(filepath.Clean(relative), string(filepath.Separator))...) {
		if component != "" && component != "." {
			path = filepath.Join(path, component)
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Join(root, relative), nil
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("image directory %q is not a real directory", path)
		}
	}
	return path, nil
}

func openLiteDirLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func seedLiteSpecial(imagePath, destination string, linux *specs.Linux) error {
	info, err := os.Lstat(destination)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("persistent special destination %q is not a real directory", destination)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(destination)+".seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	info, err = os.Lstat(imagePath)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("image special directory %q is not a real directory", imagePath)
		}
		if err := copyLiteDir(imagePath, stage, linux); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(stage, destination)
}

func copyLiteDir(source, destination string, linux *specs.Linux) error {
	var uidMappings, gidMappings []specs.LinuxIDMapping
	if linux != nil {
		uidMappings, gidMappings = linux.UIDMappings, linux.GIDMappings
	}
	uidMap, gidMap, err := liteRsyncIDMaps(source, uidMappings, gidMappings)
	if err != nil {
		return err
	}
	args := []string{"-aHAX", "--numeric-ids", "--one-file-system", "--exclude=/lost+found", "--exclude=/" + liteVolumeInitMarker}
	if uidMap != "" {
		args = append(args, "--usermap="+uidMap)
	}
	if gidMap != "" {
		args = append(args, "--groupmap="+gidMap)
	}
	args = append(args, source+string(filepath.Separator), destination+string(filepath.Separator))
	output, err := exec.Command("rsync", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy %q to %q: %w: %s", source, destination, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func liteRsyncIDMaps(root string, uidMappings, gidMappings []specs.LinuxIDMapping) (string, string, error) {
	uids, gids := map[uint32]struct{}{}, map[uint32]struct{}{}
	if err := filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		var stat unix.Stat_t
		if err := unix.Lstat(path, &stat); err != nil {
			return err
		}
		uids[stat.Uid], gids[stat.Gid] = struct{}{}, struct{}{}
		return nil
	}); err != nil {
		return "", "", err
	}
	uidMap, err := liteRsyncIDMap(uids, uidMappings)
	if err != nil {
		return "", "", err
	}
	gidMap, err := liteRsyncIDMap(gids, gidMappings)
	return uidMap, gidMap, err
}

func liteRsyncIDMap(ids map[uint32]struct{}, mappings []specs.LinuxIDMapping) (string, error) {
	var from []uint32
	for id := range ids {
		from = append(from, id)
	}
	sort.Slice(from, func(i, j int) bool { return from[i] < from[j] })
	var pairs []string
	for _, id := range from {
		for _, mapping := range mappings {
			if mapping.Size == 0 || uint64(mapping.HostID)+uint64(mapping.Size) > 1<<32 || uint64(mapping.ContainerID)+uint64(mapping.Size) > 1<<32 {
				return "", fmt.Errorf("invalid UID/GID mapping")
			}
			if id >= mapping.HostID && uint64(id) < uint64(mapping.HostID)+uint64(mapping.Size) {
				mapped := mapping.ContainerID + (id - mapping.HostID)
				if mapped != id {
					pairs = append(pairs, strconv.FormatUint(uint64(id), 10)+":"+strconv.FormatUint(uint64(mapped), 10))
				}
				break
			}
		}
	}
	return strings.Join(pairs, ","), nil
}
