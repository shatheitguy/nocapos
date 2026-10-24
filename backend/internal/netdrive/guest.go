package netdrive

import (
	"path"
	"strings"

	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// Guest access: a shared folder can be opened by anyone on the network
// without a password, read only or read and write. Over SMB that's Samba's
// "guest ok" (see RenderSamba); over WebDAV a request without a sign-in may
// open /dav/<Folder>/… for exactly that folder, nothing else.

// davGuest returns the one guest folder an unsigned request asks for (nil
// when the path doesn't start with a folder open to guests).
func (m *Manager) davGuest(urlPath string) []davEntry {
	if m.spaces == nil {
		return nil
	}
	rest := strings.TrimPrefix(path.Clean("/"+urlPath), DAVPrefix)
	first, _, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	if first == "" {
		return nil // the top level lists people's folders: sign in for that
	}
	for _, f := range m.spaces.NetFolders(nil) {
		g := guestMode(f.SharedFolder)
		if g == "" || !strings.EqualFold(f.Name, first) {
			continue
		}
		return []davEntry{{name: f.Name, root: spaces.ShareRootID(f.ID), path: f.FullPath, write: g == store.GuestRW, recycle: f.Recycle, listed: true}}
	}
	return nil
}
