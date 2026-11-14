package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/mcp"
	"alfaos/alfad/internal/store"
)

// NoCap MCP's tools. Most reuse the same handlers the NoCapOS apps call,
// run as the agent's owner, so every permission check, index update and
// audit entry is exactly what the person would get clicking through. On
// top of that, each tool needs a level of one capability in the agent's
// scopes (deny by default), and file and photo tools stay inside the
// agent's folders.

type agentTool struct {
	Name        string
	Title       string
	Description string
	Props       []mcp.Prop
	Cap, Need   string // the capability and the least level of it this tool needs
	action      bool   // changes something: the owner approves each call (unless relaxed)
	admin       bool   // only for administrators' agents
	summary     func(args map[string]any) string
	run         func(c *toolCall) mcp.Result
}

func (t *agentTool) describe() mcp.Tool {
	ann := map[string]any{"title": t.Title, "readOnlyHint": !t.action}
	if t.action {
		ann["destructiveHint"] = true
	}
	desc := t.Description
	if t.action {
		desc += " May need the owner's approval in NoCapOS (the call then waits up to 2 minutes)."
	}
	return mcp.Tool{Name: t.Name, Title: t.Title, Description: desc, InputSchema: mcp.Schema(t.Props...), Annotations: ann}
}

func (t *agentTool) summarize(args map[string]any) string {
	if t.summary != nil {
		return t.summary(args)
	}
	return t.Title
}

func findAgentTool(name string) *agentTool {
	for i := range agentTools {
		if agentTools[i].Name == name {
			return &agentTools[i]
		}
	}
	return nil
}

// toolCall is one tool run.
type toolCall struct {
	s    *Server
	ctx  context.Context
	b    *agentBackend
	sc   mcp.Scopes
	args map[string]any
}

func (c *toolCall) str(name string) string { return strings.TrimSpace(mcp.Str(c.args, name)) }

// inFolders checks root:p against the agent's folders.
func (c *toolCall) inFolders(root, p string) *mcp.Result {
	if c.sc.FolderAllows(root, p) {
		return nil
	}
	r := mcp.Errorf("This agent may only use these folders: %s", strings.Join(c.sc.Folders, ", "))
	return &r
}

// invoke calls an API handler as the agent's owner and returns its answer.
func (c *toolCall) invoke(h http.HandlerFunc, method, target string, body any, pathValues map[string]string) (int, []byte) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r, err := http.NewRequestWithContext(context.WithValue(c.ctx, userKey, c.b.u), method, target, rd)
	if err != nil {
		return http.StatusBadRequest, []byte(`{"error":{"message":"bad request"}}`)
	}
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = c.b.r.RemoteAddr
	r.Host = c.b.r.Host
	for k, v := range pathValues {
		r.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// maxToolText caps any text a tool hands back.
const maxToolText = 256 << 10

func capText(s string) string {
	if len(s) > maxToolText {
		return s[:maxToolText] + fmt.Sprintf("\n… (cut: %d more bytes)", len(s)-maxToolText)
	}
	return s
}

// answer turns a handler's response into a tool result.
func answer(status int, body []byte) mcp.Result {
	if status >= 400 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			return mcp.Errorf("%s", e.Error.Message)
		}
		return mcp.Errorf("failed (HTTP %d)", status)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return mcp.Text("Done.")
	}
	var v any
	if json.Unmarshal(body, &v) == nil {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			return mcp.Text(capText(string(b)))
		}
	}
	return mcp.Text(capText(string(body)))
}

func q(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			v.Set(kv[i], kv[i+1])
		}
	}
	return v.Encode()
}

func (c *toolCall) get(h http.HandlerFunc, path string) (any, error) {
	status, body := c.invoke(h, http.MethodGet, path, nil, nil)
	res := answer(status, body)
	if res.IsError {
		return nil, errors.New(res.Content[0].Text)
	}
	var v any
	_ = json.Unmarshal(body, &v)
	return v, nil
}

// combined runs several reads and reports each part (or why it failed).
func (c *toolCall) combined(parts map[string]func() (any, error)) mcp.Result {
	out := map[string]any{}
	for name, fn := range parts {
		v, err := fn()
		if err != nil {
			out[name] = map[string]string{"unavailable": err.Error()}
			continue
		}
		out[name] = v
	}
	return mcp.JSON(out)
}

var (
	propRoot = mcp.Prop{Name: "root", Type: "string", Required: true,
		Description: "Storage location id, from list_files with no root."}
	propPath = mcp.Prop{Name: "path", Type: "string", Required: true,
		Description: "Path inside the location, like /Documents/notes.txt."}
	propPhotoRoot = mcp.Prop{Name: "root", Type: "string", Required: true, Description: "The photo's location id, from list_photos."}
	propPhotoPath = mcp.Prop{Name: "path", Type: "string", Required: true, Description: "The photo's path, from list_photos."}
)

func fileTarget(args map[string]any) string {
	return mcp.Str(args, "root") + ":" + mcp.Str(args, "path")
}

// filterList keeps the JSON array items keep accepts.
func filterList(body []byte, field string, keep func(map[string]any) bool) ([]byte, bool) {
	var whole any
	if json.Unmarshal(body, &whole) != nil {
		return nil, false
	}
	pick := func(list []any) []any {
		out := []any{}
		for _, it := range list {
			if m, ok := it.(map[string]any); ok && keep(m) {
				out = append(out, m)
			}
		}
		return out
	}
	switch v := whole.(type) {
	case []any:
		whole = pick(v)
	case map[string]any:
		list, ok := v[field].([]any)
		if !ok {
			return nil, false
		}
		v[field] = pick(list)
	default:
		return nil, false
	}
	b, err := json.Marshal(whole)
	return b, err == nil
}

func strField(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func onOff(b bool, on, off string) string {
	if b {
		return on
	}
	return off
}

var agentTools = []agentTool{
	// ---- Data: logs, files, photos ----
	{
		Name: "system_logs", Title: "NoCapOS logs", Cap: mcp.CapLogs, Need: mcp.Read, admin: true,
		Description: "NoCapOS's own recent log lines from the system journal.",
		Props:       []mcp.Prop{{Name: "lines", Type: "integer", Description: "How many lines (1-2000, default 300)."}},
		run: func(c *toolCall) mcp.Result {
			n := min(max(mcp.Int(c.args, "lines", 300), 1), 2000)
			return answer(c.invoke(c.s.systemLogs, http.MethodGet, "/api/v1/system/logs?lines="+strconv.Itoa(n), nil, nil))
		},
	},
	{
		Name: "container_logs", Title: "Container logs", Cap: mcp.CapLogs, Need: mcp.Read, admin: true,
		Description: "The last lines a container printed.",
		Props: []mcp.Prop{
			{Name: "container", Type: "string", Required: true, Description: "Container name or id."},
			{Name: "lines", Type: "integer", Description: "How many lines (1-1000, default 100)."},
		},
		run: func(c *toolCall) mcp.Result {
			id := c.str("container")
			if !validContainerRef(id) {
				return mcp.Errorf("that isn't a container name or id")
			}
			n := min(max(mcp.Int(c.args, "lines", 100), 1), 1000)
			ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
			defer cancel()
			var b strings.Builder
			err := c.s.Docker.Logs(ctx, id, n, func(l docker.LogLine) error {
				b.WriteString(l.Text)
				b.WriteByte('\n')
				return nil
			})
			if err != nil && ctx.Err() == nil {
				return mcp.Errorf("couldn't read the logs: %v", err)
			}
			if b.Len() == 0 {
				return mcp.Text("(no output)")
			}
			return mcp.Text(capText(b.String()))
		},
	},
	{
		Name: "list_files", Title: "List files", Cap: mcp.CapFiles, Need: mcp.Read,
		Description: "With no root: the storage locations you can open. With a root: the files and folders at path.",
		Props: []mcp.Prop{
			{Name: "root", Type: "string", Description: "Storage location id. Leave out to list locations."},
			{Name: "path", Type: "string", Description: "Folder inside the location (default /)."},
		},
		run: func(c *toolCall) mcp.Result {
			root := c.str("root")
			if root == "" {
				status, body := c.invoke(c.s.filesRoots, http.MethodGet, "/api/v1/files/roots", nil, nil)
				if roots := c.sc.FolderRoots(); roots != nil && status < 400 {
					if b, ok := filterList(body, "", func(m map[string]any) bool {
						for _, r := range roots {
							if strField(m, "id") == r {
								return true
							}
						}
						return false
					}); ok {
						body = b
					}
				}
				return answer(status, body)
			}
			if bad := c.inFolders(root, c.str("path")); bad != nil {
				return *bad
			}
			return answer(c.invoke(c.s.filesList, http.MethodGet, "/api/v1/files/list?"+q("root", root, "path", c.str("path")), nil, nil))
		},
	},
	{
		Name: "read_file", Title: "Read a file", Cap: mcp.CapFiles, Need: mcp.Read,
		Description: "Reads a text file (up to 256 KB is returned).",
		Props:       []mcp.Prop{propRoot, propPath},
		run: func(c *toolCall) mcp.Result {
			if bad := c.inFolders(c.str("root"), c.str("path")); bad != nil {
				return *bad
			}
			status, body := c.invoke(c.s.filesReadText, http.MethodGet, "/api/v1/files/text?"+q("root", c.str("root"), "path", c.str("path")), nil, nil)
			if status >= 400 {
				return answer(status, body)
			}
			var f struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(body, &f)
			return mcp.Text(capText(f.Content))
		},
	},
	{
		Name: "search_files", Title: "Search files", Cap: mcp.CapFiles, Need: mcp.Read,
		Description: "Finds files and folders by name across the locations you can open.",
		Props: []mcp.Prop{
			{Name: "query", Type: "string", Required: true, Description: "Words in the name."},
			{Name: "limit", Type: "integer", Description: "Most results (1-100, default 20)."},
		},
		run: func(c *toolCall) mcp.Result {
			limit := strconv.Itoa(mcp.Int(c.args, "limit", 20))
			status, body := c.invoke(c.s.filesSearch, http.MethodGet, "/api/v1/files/search?"+q("q", c.str("query"), "limit", limit), nil, nil)
			if len(c.sc.Folders) > 0 && status < 400 {
				b, ok := filterList(body, "results", func(m map[string]any) bool {
					return c.sc.FolderAllows(strField(m, "root"), strField(m, "path"))
				})
				if !ok {
					return mcp.Errorf("couldn't limit the results to this agent's folders")
				}
				body = b
			}
			return answer(status, body)
		},
	},
	{
		Name: "write_file", Title: "Write a file", Cap: mcp.CapFiles, Need: mcp.Write, action: true,
		Description: "Creates or replaces a text file (up to 2 MB).",
		Props: []mcp.Prop{propRoot, propPath,
			{Name: "content", Type: "string", Required: true, Description: "The whole new text of the file."},
		},
		summary: func(a map[string]any) string {
			return fmt.Sprintf("Write %d characters to %s", len(mcp.Str(a, "content")), fileTarget(a))
		},
		run: func(c *toolCall) mcp.Result {
			if bad := c.inFolders(c.str("root"), c.str("path")); bad != nil {
				return *bad
			}
			return answer(c.invoke(c.s.filesWriteText, http.MethodPut, "/api/v1/files/text", map[string]any{
				"root": c.str("root"), "path": c.str("path"), "content": mcp.Str(c.args, "content"),
			}, nil))
		},
	},
	{
		Name: "move_file", Title: "Move or rename a file", Cap: mcp.CapFiles, Need: mcp.Write, action: true,
		Description: "Moves a file or folder into another folder in the same location, and/or renames it.",
		Props: []mcp.Prop{propRoot, propPath,
			{Name: "to_folder", Type: "string", Description: "Folder to move it into."},
			{Name: "new_name", Type: "string", Description: "New name (no slashes)."},
		},
		summary: func(a map[string]any) string {
			s := "Move " + fileTarget(a)
			if f := mcp.Str(a, "to_folder"); f != "" {
				s += " into " + f
			}
			if n := mcp.Str(a, "new_name"); n != "" {
				s += " as " + n
			}
			return s
		},
		run: func(c *toolCall) mcp.Result {
			root, p, to, name := c.str("root"), c.str("path"), c.str("to_folder"), c.str("new_name")
			if to == "" && name == "" {
				return mcp.Errorf("give to_folder, new_name or both")
			}
			if bad := c.inFolders(root, p); bad != nil {
				return *bad
			}
			if to != "" {
				if bad := c.inFolders(root, strings.TrimRight(to, "/")+"/x"); bad != nil {
					return *bad
				}
				status, body := c.invoke(c.s.touchIndex(c.s.filesTransfer), http.MethodPost, "/api/v1/files/transfer", map[string]any{
					"root": root, "paths": []string{p}, "dest": to, "move": true, "conflict": "skip",
				}, nil)
				if status >= 400 {
					return answer(status, body)
				}
				p = strings.TrimRight(to, "/") + "/" + p[strings.LastIndex(p, "/")+1:]
			}
			if name != "" {
				return answer(c.invoke(c.s.filesRename, http.MethodPost, "/api/v1/files/rename", map[string]any{"root": root, "path": p, "name": name}, nil))
			}
			return mcp.Text("Moved to " + p)
		},
	},
	{
		Name: "delete_file", Title: "Delete a file", Cap: mcp.CapFiles, Need: mcp.Write, action: true,
		Description: "Moves a file or folder to the recycle bin (or deletes it for good with permanent).",
		Props: []mcp.Prop{propRoot, propPath,
			{Name: "permanent", Type: "boolean", Description: "Skip the recycle bin."},
		},
		summary: func(a map[string]any) string {
			if mcp.Bool(a, "permanent") {
				return "Delete " + fileTarget(a) + " for good"
			}
			return "Move " + fileTarget(a) + " to the recycle bin"
		},
		run: func(c *toolCall) mcp.Result {
			if bad := c.inFolders(c.str("root"), c.str("path")); bad != nil {
				return *bad
			}
			return answer(c.invoke(c.s.filesDelete, http.MethodPost, "/api/v1/files/delete", map[string]any{
				"root": c.str("root"), "paths": []string{c.str("path")}, "permanent": mcp.Bool(c.args, "permanent"),
			}, nil))
		},
	},
	{
		Name: "list_photos", Title: "List photos", Cap: mcp.CapPhotos, Need: mcp.Read,
		Description: "Photos and videos in the Photos library, newest first.",
		Props:       []mcp.Prop{{Name: "limit", Type: "integer", Description: "Most results (1-500, default 100)."}},
		run: func(c *toolCall) mcp.Result {
			status, body := c.invoke(c.s.photosList, http.MethodGet, "/api/v1/photos", nil, nil)
			if status >= 400 {
				return answer(status, body)
			}
			if len(c.sc.Folders) > 0 {
				b, ok := filterList(body, "items", func(m map[string]any) bool {
					return c.sc.FolderAllows(strField(m, "root"), strField(m, "path"))
				})
				if !ok {
					return mcp.Errorf("couldn't limit the photos to this agent's folders")
				}
				body = b
			}
			var v map[string]any
			if json.Unmarshal(body, &v) == nil {
				if items, ok := v["items"].([]any); ok {
					if n := min(max(mcp.Int(c.args, "limit", 100), 1), 500); len(items) > n {
						v["items"], v["more"] = items[:n], len(items)-n
					}
				}
				return mcp.JSON(v)
			}
			return answer(status, body)
		},
	},
	{
		Name: "favorite_photo", Title: "Favorite a photo", Cap: mcp.CapPhotos, Need: mcp.Write, action: true,
		Description: "Marks a photo as a favorite in Photos, or unmarks it.",
		Props: []mcp.Prop{propPhotoRoot, propPhotoPath,
			{Name: "favorite", Type: "boolean", Description: "true to favorite (default), false to unfavorite."},
		},
		summary: func(a map[string]any) string {
			_, set := a["favorite"]
			return onOff(!set || mcp.Bool(a, "favorite"), "Favorite ", "Unfavorite ") + fileTarget(a)
		},
		run: func(c *toolCall) mcp.Result {
			if bad := c.inFolders(c.str("root"), c.str("path")); bad != nil {
				return *bad
			}
			_, set := c.args["favorite"]
			on := !set || mcp.Bool(c.args, "favorite")
			return answer(c.invoke(c.s.photosFavorite, http.MethodPost, "/api/v1/photos/favorite", map[string]any{
				"items": []map[string]string{{"root": c.str("root"), "path": c.str("path")}}, "on": on,
			}, nil))
		},
	},
	{
		Name: "delete_photo", Title: "Delete a photo", Cap: mcp.CapPhotos, Need: mcp.Write, action: true,
		Description: "Moves a photo or video to the recycle bin.",
		Props:       []mcp.Prop{propPhotoRoot, propPhotoPath},
		summary:     func(a map[string]any) string { return "Move the photo " + fileTarget(a) + " to the recycle bin" },
		run: func(c *toolCall) mcp.Result {
			if bad := c.inFolders(c.str("root"), c.str("path")); bad != nil {
				return *bad
			}
			return answer(c.invoke(c.s.touchIndex(c.s.photosDelete), http.MethodPost, "/api/v1/photos/delete", map[string]any{
				"items": []map[string]string{{"root": c.str("root"), "path": c.str("path")}},
			}, nil))
		},
	},

	// ---- System: info, network, users, screen ----
	{
		Name: "system_info", Title: "System info", Cap: mcp.CapSystem, Need: mcp.View,
		Description: "This NoCapOS server: hostname, OS, CPU, memory, uptime, versions and hardware.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.systemInfo, http.MethodGet, "/api/v1/system/info", nil, nil))
		},
	},
	{
		Name: "storage_status", Title: "Storage status", Cap: mcp.CapSystem, Need: mcp.View, admin: true,
		Description: "Disks, their health and ZFS pools.",
		run: func(c *toolCall) mcp.Result {
			return c.combined(map[string]func() (any, error){
				"tools": func() (any, error) { return c.get(c.s.stStatus, "/api/v1/storage/status") },
				"disks": func() (any, error) { return c.get(c.s.stDisks, "/api/v1/storage/disks") },
				"pools": func() (any, error) { return c.get(c.s.stPools, "/api/v1/storage/pools") },
			})
		},
	},
	{
		Name: "set_hostname", Title: "Rename the server", Cap: mcp.CapSystem, Need: mcp.Change, action: true, admin: true,
		Description: "Changes this server's hostname (its name on the network).",
		Props:       []mcp.Prop{{Name: "hostname", Type: "string", Required: true, Description: "New name: letters, digits and dashes."}},
		summary:     func(a map[string]any) string { return "Rename the server to " + mcp.Str(a, "hostname") },
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.hostnameSet, http.MethodPut, "/api/v1/system/hostname", map[string]string{"hostname": c.str("hostname")}, nil))
		},
	},
	{
		Name: "network_status", Title: "Network status", Cap: mcp.CapNetwork, Need: mcp.View, admin: true,
		Description: "Network adapters, Wi-Fi and IP addresses.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.networkState, http.MethodGet, "/api/v1/system/network", nil, nil))
		},
	},
	{
		Name: "wifi_connect", Title: "Join a Wi-Fi network", Cap: mcp.CapNetwork, Need: mcp.Change, action: true, admin: true,
		Description: "Connects the server to a Wi-Fi network. It may lose its connection while switching.",
		Props: []mcp.Prop{
			{Name: "ssid", Type: "string", Required: true, Description: "Network name."},
			{Name: "password", Type: "string", Description: "Its password (left out for open networks)."},
			{Name: "device", Type: "string", Description: "Wi-Fi adapter (default: the first one)."},
		},
		summary: func(a map[string]any) string { return "Join the Wi-Fi network " + mcp.Str(a, "ssid") },
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.wifiConnect, http.MethodPost, "/api/v1/system/wifi/connect", map[string]any{
				"ssid": c.str("ssid"), "password": mcp.Str(c.args, "password"), "device": c.str("device"),
			}, nil))
		},
	},
	{
		Name: "wifi_disconnect", Title: "Leave Wi-Fi", Cap: mcp.CapNetwork, Need: mcp.Change, action: true, admin: true,
		Description: "Disconnects a Wi-Fi adapter. The server may become unreachable.",
		Props:       []mcp.Prop{{Name: "device", Type: "string", Required: true, Description: "Wi-Fi adapter, from network_status."}},
		summary:     func(a map[string]any) string { return "Disconnect Wi-Fi on " + mcp.Str(a, "device") },
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.wifiDisconnect, http.MethodPost, "/api/v1/system/wifi/disconnect", map[string]string{"device": c.str("device")}, nil))
		},
	},
	{
		Name: "list_users", Title: "List users", Cap: mcp.CapUsers, Need: mcp.View, admin: true,
		Description: "The people who can sign in to NoCapOS, their roles and whether they're disabled.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.usersList, http.MethodGet, "/api/v1/users", nil, nil))
		},
	},
	{
		Name: "set_user_disabled", Title: "Disable or enable a user", Cap: mcp.CapUsers, Need: mcp.Manage, action: true, admin: true,
		Description: "Stops a person from signing in (disabled) or lets them back in.",
		Props: []mcp.Prop{
			{Name: "username", Type: "string", Required: true, Description: "Their username, from list_users."},
			{Name: "disabled", Type: "boolean", Required: true, Description: "true to disable, false to enable."},
		},
		summary: func(a map[string]any) string {
			return onOff(mcp.Bool(a, "disabled"), "Disable ", "Enable ") + "the user " + mcp.Str(a, "username")
		},
		run: func(c *toolCall) mcp.Result {
			name := c.str("username")
			disabled := mcp.Bool(c.args, "disabled")
			return answer(c.invoke(c.s.usersUpdate, http.MethodPatch, "/api/v1/users/"+url.PathEscape(name),
				map[string]any{"disabled": disabled}, map[string]string{"name": name}))
		},
	},
	{
		Name: "screenshot", Title: "Screenshot", Cap: mcp.CapScreen, Need: mcp.View, admin: true,
		Description: "A picture of NoCap Desktop on the server's own screen (when it's running).",
		Props: []mcp.Prop{
			{Name: "scale", Type: "number", Description: "Shrink the picture, 0.25 to 1 (default 1)."},
		},
		run: func(c *toolCall) mcp.Result {
			scale := 1.0
			if f, ok := c.args["scale"].(float64); ok && f >= 0.25 && f <= 1 {
				scale = f
			}
			png, err := c.s.agentScreen().Screenshot(c.ctx, scale)
			if err != nil {
				return mcp.Errorf("%v", err)
			}
			return mcp.Result{Content: []mcp.Content{{Type: "image", Data: base64.StdEncoding.EncodeToString(png), MimeType: "image/png"}}}
		},
	},
	{
		Name: "open_url", Title: "Open a web page on the screen", Cap: mcp.CapScreen, Need: mcp.Control, action: true, admin: true,
		Description: "Opens an http(s) address on NoCap Desktop, the server's own screen.",
		Props:       []mcp.Prop{{Name: "url", Type: "string", Required: true, Description: "http:// or https:// address."}},
		summary:     func(a map[string]any) string { return "Open " + mcp.Str(a, "url") + " on the server's screen" },
		run: func(c *toolCall) mcp.Result {
			if err := c.s.agentScreen().OpenURL(c.ctx, c.str("url")); err != nil {
				return mcp.Errorf("%v", err)
			}
			return mcp.Text("Opened.")
		},
	},
	{
		Name: "type_text", Title: "Type on the screen", Cap: mcp.CapScreen, Need: mcp.Control, action: true, admin: true,
		Description: "Types text into whatever has focus on NoCap Desktop (needs wtype or ydotool on the server).",
		Props:       []mcp.Prop{{Name: "text", Type: "string", Required: true, Description: "Up to 2000 characters."}},
		summary: func(a map[string]any) string {
			return "Type " + strconv.Quote(mcp.Str(a, "text")) + " on the server's screen"
		},
		run: func(c *toolCall) mcp.Result {
			if err := c.s.agentScreen().TypeText(c.ctx, mcp.Str(c.args, "text")); err != nil {
				return mcp.Errorf("%v", err)
			}
			return mcp.Text("Typed.")
		},
	},
	{
		Name: "click", Title: "Click on the screen", Cap: mcp.CapScreen, Need: mcp.Control, action: true, admin: true,
		Description: "Clicks at a point on NoCap Desktop (needs ydotool on the server). Take a screenshot first to find the spot.",
		Props: []mcp.Prop{
			{Name: "x", Type: "integer", Required: true, Description: "Pixels from the left."},
			{Name: "y", Type: "integer", Required: true, Description: "Pixels from the top."},
			{Name: "button", Type: "string", Description: "Mouse button (default left).", Enum: []string{"left", "right", "middle"}},
		},
		summary: func(a map[string]any) string {
			return fmt.Sprintf("Click (%s) at %d, %d on the server's screen", orDefault(mcp.Str(a, "button"), "left"), mcp.Int(a, "x", 0), mcp.Int(a, "y", 0))
		},
		run: func(c *toolCall) mcp.Result {
			if err := c.s.agentScreen().Click(c.ctx, mcp.Int(c.args, "x", -1), mcp.Int(c.args, "y", -1), c.str("button")); err != nil {
				return mcp.Errorf("%v", err)
			}
			return mcp.Text("Clicked.")
		},
	},

	// ---- Apps & containers ----
	{
		Name: "list_apps", Title: "List apps", Cap: mcp.CapApps, Need: mcp.View, admin: true,
		Description: "Installed App Store apps (with their state) and installed Linux apps.",
		run: func(c *toolCall) mcp.Result {
			return c.combined(map[string]func() (any, error){
				"app_store":  func() (any, error) { return c.get(c.s.appstoreList, "/api/v1/appstore") },
				"linux_apps": func() (any, error) { return c.get(c.s.nativeInstalled, "/api/v1/native-apps/installed") },
			})
		},
	},
	{
		Name: "app_action", Title: "Install or control an app", Cap: mcp.CapApps, Need: mcp.Manage, action: true, admin: true,
		Description: "Installs, starts, stops, restarts, updates or uninstalls (keeping its data) an App Store app.",
		Props: []mcp.Prop{
			{Name: "app", Type: "string", Required: true, Description: "App Store app id, from list_apps."},
			{Name: "action", Type: "string", Required: true, Description: "What to do.", Enum: []string{"install", "start", "stop", "restart", "update", "uninstall"}},
		},
		summary: func(a map[string]any) string { return mcp.Str(a, "action") + " the app " + mcp.Str(a, "app") },
		run: func(c *toolCall) mcp.Result {
			id, act := c.str("app"), c.str("action")
			return answer(c.invoke(c.s.appstoreAction, http.MethodPost, "/api/v1/appstore/"+url.PathEscape(id)+"/"+act, nil,
				map[string]string{"id": id, "action": act}))
		},
	},
	{
		Name: "list_containers", Title: "List containers", Cap: mcp.CapContainers, Need: mcp.View, admin: true,
		Description: "Every container with its image, state and ports.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.listContainers, http.MethodGet, "/api/v1/containers", nil, nil))
		},
	},
	{
		Name: "container_action", Title: "Start, stop or restart a container", Cap: mcp.CapContainers, Need: mcp.Manage, action: true, admin: true,
		Description: "Starts, stops or restarts a container.",
		Props: []mcp.Prop{
			{Name: "container", Type: "string", Required: true, Description: "Container name or id."},
			{Name: "action", Type: "string", Required: true, Description: "What to do.", Enum: []string{"start", "stop", "restart"}},
		},
		summary: func(a map[string]any) string {
			return mcp.Str(a, "action") + " the container " + mcp.Str(a, "container")
		},
		run: func(c *toolCall) mcp.Result {
			id, act := c.str("container"), c.str("action")
			return answer(c.invoke(c.s.containerAction, http.MethodPost, "/api/v1/containers/"+url.PathEscape(id)+"/"+act, nil,
				map[string]string{"id": id, "action": act}))
		},
	},
	{
		Name: "list_vms", Title: "List virtual machines", Cap: mcp.CapContainers, Need: mcp.View, admin: true,
		Description: "Virtual Desk's virtual machines and whether each is running.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.vmList, http.MethodGet, "/api/v1/vms", nil, nil))
		},
	},
	{
		Name: "vm_action", Title: "Start or stop a virtual machine", Cap: mcp.CapContainers, Need: mcp.Manage, action: true, admin: true,
		Description: "Starts a virtual machine, shuts it down (asks the guest), powers it off or reboots it.",
		Props: []mcp.Prop{
			{Name: "vm", Type: "string", Required: true, Description: "Virtual machine name, from list_vms."},
			{Name: "action", Type: "string", Required: true, Description: "What to do.", Enum: []string{"start", "shutdown", "poweroff", "reboot"}},
		},
		summary: func(a map[string]any) string {
			return mcp.Str(a, "action") + " the virtual machine " + mcp.Str(a, "vm")
		},
		run: func(c *toolCall) mcp.Result {
			name := c.str("vm")
			return answer(c.invoke(c.s.vmPower, http.MethodPost, "/api/v1/vms/"+url.PathEscape(name)+"/power",
				map[string]string{"action": c.str("action")}, map[string]string{"name": name}))
		},
	},

	// ---- Power & danger zone ----
	{
		Name: "run_command", Title: "Run a command", Cap: mcp.CapTerminal, Need: mcp.On, action: true, admin: true,
		Description: "Runs a shell command on the server as your own Linux account (never root) and returns its output (up to 64 KB).",
		Props: []mcp.Prop{
			{Name: "command", Type: "string", Required: true, Description: "The command line, run with /bin/sh -c."},
			{Name: "timeout_seconds", Type: "integer", Description: "Stop it after this long (default 60, most 600)."},
		},
		summary: func(a map[string]any) string { return "Run: " + mcp.Str(a, "command") },
		run: func(c *toolCall) mcp.Result {
			cmd := mcp.Str(c.args, "command")
			if strings.TrimSpace(cmd) == "" || len(cmd) > 16<<10 {
				return mcp.Errorf("command must be 1 character to 16 KB")
			}
			acct, err := c.s.agentAccount(c.b)
			if err != nil {
				return mcp.Errorf("%v", err)
			}
			return runResult(mcp.RunCommand(c.ctx, acct, cmd, mcp.ClampTimeout(mcp.Int(c.args, "timeout_seconds", 0)), mcp.MaxRunOutput))
		},
	},
	{
		Name: "run_code", Title: "Run code", Cap: mcp.CapCode, Need: mcp.Sandbox, action: true,
		Description: "Runs a python, node or sh snippet and returns its output (up to 64 KB). " +
			"where=sandbox (default) runs it in a throwaway sandbox with no network and no access to your files; " +
			"where=host runs it on the server as your own Linux account (never root), when this agent is allowed to.",
		Props: []mcp.Prop{
			{Name: "language", Type: "string", Required: true, Description: "The snippet's language.", Enum: mcp.CodeLanguages},
			{Name: "code", Type: "string", Required: true, Description: "The code (up to 64 KB)."},
			{Name: "where", Type: "string", Description: "sandbox (default) or host.", Enum: []string{mcp.Sandbox, mcp.Host}},
			{Name: "timeout_seconds", Type: "integer", Description: "Stop it after this long (default 60, most 600)."},
		},
		summary: func(a map[string]any) string {
			code := mcp.Str(a, "code")
			lines := strings.Count(code, "\n") + 1
			if i := strings.IndexByte(code, '\n'); i >= 0 {
				code = code[:i] + " …"
			}
			return fmt.Sprintf("Run %s code (%s, %d lines): %s", mcp.Str(a, "language"), orDefault(mcp.Str(a, "where"), mcp.Sandbox), lines, code)
		},
		run: func(c *toolCall) mcp.Result {
			lang, code := c.str("language"), mcp.Str(c.args, "code")
			timeout := mcp.ClampTimeout(mcp.Int(c.args, "timeout_seconds", 0))
			if orDefault(c.str("where"), mcp.Sandbox) == mcp.Host {
				if !c.sc.Allows(mcp.CapCode, mcp.Host) {
					return mcp.Errorf("This agent may only run code in the sandbox.")
				}
				acct, err := c.s.agentAccount(c.b)
				if err != nil {
					return mcp.Errorf("%v", err)
				}
				return runResult(mcp.RunCode(c.ctx, acct, lang, code, timeout, mcp.MaxRunOutput))
			}
			return runResult(mcp.RunSandboxed(c.ctx, lang, code, timeout, mcp.MaxRunOutput))
		},
	},
	{
		Name: "power", Title: "Restart or shut down", Cap: mcp.CapPower, Need: mcp.On, action: true, admin: true,
		Description: "Restarts or shuts down the whole server. Always needs the owner's approval.",
		Props:       []mcp.Prop{{Name: "action", Type: "string", Required: true, Description: "What to do.", Enum: []string{"reboot", "shutdown"}}},
		summary: func(a map[string]any) string {
			return onOff(mcp.Str(a, "action") == "shutdown", "Shut down the server", "Restart the server")
		},
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.powerAction, http.MethodPost, "/api/v1/system/power", map[string]string{"action": c.str("action")}, nil))
		},
	},
}

var errAgentHostAdminOnly = errors.New("only administrators can let an agent run commands on the server")

func runResult(res mcp.RunResult, err error) mcp.Result {
	if err != nil {
		return mcp.Errorf("%v", err)
	}
	r := mcp.JSON(res)
	r.IsError = res.TimedOut
	return r
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// agentAccount is the Linux account run_command and host code use: the
// owner's own one when NoCapOS runs as root, NoCapOS's own (unprivileged)
// one otherwise. Never root.
func (s *Server) agentAccount(b *agentBackend) (mcp.Account, error) {
	// A command on the host is a terminal, and the terminal is for
	// administrators only; on a non-root install it would also run as the
	// account that owns NoCapOS's own data.
	if b == nil || b.u == nil || b.u.Role != store.RoleAdmin {
		return mcp.Account{}, errAgentHostAdminOnly
	}
	if runtime.GOOS != "linux" {
		return mcp.Account{}, mcp.ErrRunUnsupported
	}
	if s.Terminal == nil || !s.Terminal.RunsAsRoot() {
		cur, err := user.Current()
		if err != nil {
			return mcp.Account{}, mcp.ErrRunUnsupported
		}
		uid, _ := strconv.ParseUint(cur.Uid, 10, 32)
		gid, _ := strconv.ParseUint(cur.Gid, 10, 32)
		if cur.Uid == "0" || os.Geteuid() == 0 {
			return mcp.Account{}, mcp.ErrRoot
		}
		return mcp.Account{Username: cur.Username, UID: uint32(uid), GID: uint32(gid), Home: cur.HomeDir}, nil
	}
	a, err := s.hostAccount(b.u)
	if err != nil {
		return mcp.Account{}, err
	}
	if a == nil || a.Root() {
		return mcp.Account{}, mcp.ErrRoot
	}
	return mcp.Account{Username: a.Username, UID: a.UID, GID: a.GID, Groups: a.Groups, Home: a.Home}, nil
}
