package cloudimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"alfaos/alfad/internal/files"
)

// Cloud mounts: an account opened as a folder (rclone mount, run as a
// transient systemd service so it stops cleanly). Files sees it as a
// location like any disk. The account's settings stay sealed in the store;
// while mounted, rclone reads them from a 0600 config inside NoCapOS's
// secrets folder, removed on unmount.

// CloudBase is where cloud accounts are mounted.
const CloudBase = "/srv/nocapos/cloud"

// Mount is one account's mount state.
type Mount struct {
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Mounted   bool   `json:"mounted"`
	Root      string `json:"root"`
}

func mountPath(id int64) string { return CloudBase + "/" + strconv.FormatInt(id, 10) }
func mountUnit(id int64) string { return "nocapos-cloud-" + strconv.FormatInt(id, 10) }
func mountRoot(id int64) string { return "cloud:" + strconv.FormatInt(id, 10) }

// mountArgs is the systemd-run command that keeps rclone mount running.
func mountArgs(bin, conf, home string, id int64, readOnly bool) []string {
	args := []string{"systemd-run", "--unit=" + mountUnit(id), "--collect", "--property=Type=notify",
		"--setenv=HOME=" + home, bin, "--config", conf, "mount", "src:", mountPath(id),
		"--vfs-cache-mode", "writes", "--allow-other", "--dir-cache-time", "5m", "--umask", "002"}
	if readOnly {
		args = append(args, "--read-only")
	}
	return args
}

func unmountArgs(id int64) []string {
	return []string{"systemctl", "stop", mountUnit(id) + ".service"}
}

func mountConf(r *Rclone, id int64) string {
	return filepath.Join(r.TmpDir, "mount-"+strconv.FormatInt(id, 10)+".conf")
}

// Mounts lists every account and whether it's open as a folder.
func (m *Manager) Mounts(ctx context.Context) ([]Mount, error) {
	rows, err := m.st.CloudAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Mount, 0, len(rows))
	for _, a := range rows {
		out = append(out, Mount{AccountID: a.ID, Name: a.Name, Kind: a.Kind, Path: mountPath(a.ID), Mounted: mountedAt(mountPath(a.ID)), Root: mountRoot(a.ID)})
	}
	return out, nil
}

// MountAccount opens an account as a folder and adds it to Files.
func (m *Manager) MountAccount(ctx context.Context, id int64, readOnly bool) error {
	if !mountSupported() {
		return errors.New("cloud folders need NoCapOS installed directly on a Linux server")
	}
	r, err := m.tool()
	if err != nil {
		return err
	}
	rows, err := m.st.CloudAccounts(ctx)
	if err != nil {
		return err
	}
	for _, a := range rows {
		if a.ID != id {
			continue
		}
		if mountedAt(mountPath(id)) {
			return nil
		}
		rem, err := m.remote(a)
		if err != nil {
			return err
		}
		tmp, err := r.writeConfig(rem)
		if err != nil {
			return err
		}
		conf := mountConf(r, id)
		if err := os.Rename(tmp, conf); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.MkdirAll(mountPath(id), 0o755); err != nil {
			return err
		}
		if err := mountExec(ctx, mountArgs(r.Bin, conf, r.TmpDir, id, readOnly)); err != nil {
			os.Remove(conf)
			return fmt.Errorf("couldn't open %s as a folder: %w", a.Name, err)
		}
		if m.files != nil {
			m.files.AddRoot(files.Root{ID: mountRoot(id), Name: a.Name, Path: mountPath(id)})
		}
		return nil
	}
	return errors.New("no such account")
}

// UnmountAccount closes the folder (the files stay in the cloud).
func (m *Manager) UnmountAccount(ctx context.Context, id int64) error {
	if m.files != nil {
		m.files.RemoveRoot(mountRoot(id))
	}
	if !mountSupported() {
		return nil
	}
	err := mountExec(ctx, unmountArgs(id))
	if r, rerr := m.tool(); rerr == nil && r != nil {
		os.Remove(mountConf(r, id))
	}
	return err
}
