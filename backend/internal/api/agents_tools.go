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
)

// NoCap MCP's tools. Most reuse the same handlers the NoCapOS apps call,
// run as the token's owner, so every permission check, index update and
// audit entry is exactly what the person would get clicking through.

type agentTool struct {
	Name        string
	Title       string
	Description string
	Props       []mcp.Prop
	action      bool // changes something: the owner approves each call
	admin       bool // only for administrators' tokens
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
		desc += " Needs the owner's approval in NoCapOS each time (the call waits up to 2 minutes)."
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
	args map[string]any
}

func (c *toolCall) str(name string) string { return strings.TrimSpace(mcp.Str(c.args, name)) }

// invoke calls an API handler as the token's owner and returns its answer.
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
)

func fileTarget(args map[string]any) string {
	return mcp.Str(args, "root") + ":" + mcp.Str(args, "path")
}

var agentTools = []agentTool{
	// ---- looking around (runs straight away) ----
	{
		Name: "system_info", Title: "System info",
		Description: "This NoCapOS server: hostname, OS, CPU, memory, uptime, versions and hardware.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.systemInfo, http.MethodGet, "/api/v1/system/info", nil, nil))
		},
	},
	{
		Name: "list_files", Title: "List files",
		Description: "With no root: the storage locations you can open. With a root: the files and folders at path.",
		Props: []mcp.Prop{
			{Name: "root", Type: "string", Description: "Storage location id. Leave out to list locations."},
			{Name: "path", Type: "string", Description: "Folder inside the location (default /)."},
		},
		run: func(c *toolCall) mcp.Result {
			if c.str("root") == "" {
				return answer(c.invoke(c.s.filesRoots, http.MethodGet, "/api/v1/files/roots", nil, nil))
			}
			return answer(c.invoke(c.s.filesList, http.MethodGet, "/api/v1/files/list?"+q("root", c.str("root"), "path", c.str("path")), nil, nil))
		},
	},
	{
		Name: "read_file", Title: "Read a file",
		Description: "Reads a text file (up to 256 KB is returned).",
		Props:       []mcp.Prop{propRoot, propPath},
		run: func(c *toolCall) mcp.Result {
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
		Name: "search_files", Title: "Search files",
		Description: "Finds files and folders by name across the locations you can open.",
		Props: []mcp.Prop{
			{Name: "query", Type: "string", Required: true, Description: "Words in the name."},
			{Name: "limit", Type: "integer", Description: "Most results (1-100, default 20)."},
		},
		run: func(c *toolCall) mcp.Result {
			limit := strconv.Itoa(mcp.Int(c.args, "limit", 20))
			return answer(c.invoke(c.s.filesSearch, http.MethodGet, "/api/v1/files/search?"+q("q", c.str("query"), "limit", limit), nil, nil))
		},
	},
	{
		Name: "list_apps", Title: "List apps", admin: true,
		Description: "Installed App Store apps (with their state) and installed Linux apps.",
		run: func(c *toolCall) mcp.Result {
			return c.combined(map[string]func() (any, error){
				"app_store":  func() (any, error) { return c.get(c.s.appstoreList, "/api/v1/appstore") },
				"linux_apps": func() (any, error) { return c.get(c.s.nativeInstalled, "/api/v1/native-apps/installed") },
			})
		},
	},
	{
		Name: "list_containers", Title: "List containers", admin: true,
		Description: "Every Docker container with its image, state and ports.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.listContainers, http.MethodGet, "/api/v1/containers", nil, nil))
		},
	},
	{
		Name: "container_logs", Title: "Container logs", admin: true,
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
		Name: "list_vms", Title: "List virtual machines", admin: true,
		Description: "Virtual Desk's virtual machines and whether each is running.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.vmList, http.MethodGet, "/api/v1/vms", nil, nil))
		},
	},
	{
		Name: "network_status", Title: "Network status", admin: true,
		Description: "Network adapters, Wi-Fi and IP addresses.",
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.networkState, http.MethodGet, "/api/v1/system/network", nil, nil))
		},
	},
	{
		Name: "storage_status", Title: "Storage status", admin: true,
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
		Name: "screenshot", Title: "Screenshot", admin: true,
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

	// ---- doing things (the owner approves each one) ----
	{
		Name: "write_file", Title: "Write a file", action: true,
		Description: "Creates or replaces a text file (up to 2 MB).",
		Props: []mcp.Prop{propRoot, propPath,
			{Name: "content", Type: "string", Required: true, Description: "The whole new text of the file."},
		},
		summary: func(a map[string]any) string {
			return fmt.Sprintf("Write %d characters to %s", len(mcp.Str(a, "content")), fileTarget(a))
		},
		run: func(c *toolCall) mcp.Result {
			return answer(c.invoke(c.s.filesWriteText, http.MethodPut, "/api/v1/files/text", map[string]any{
				"root": c.str("root"), "path": c.str("path"), "content": mcp.Str(c.args, "content"),
			}, nil))
		},
	},
	{
		Name: "move_file", Title: "Move or rename a file",
		Description: "Moves a file or folder into another folder in the same location, and/or renames it.", action: true,
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
			if to != "" {
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
		Name: "delete_file", Title: "Delete a file", action: true,
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
			return answer(c.invoke(c.s.filesDelete, http.MethodPost, "/api/v1/files/delete", map[string]any{
				"root": c.str("root"), "paths": []string{c.str("path")}, "permanent": mcp.Bool(c.args, "permanent"),
			}, nil))
		},
	},
	{
		Name: "app_action", Title: "Install or control an app", action: true, admin: true,
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
		Name: "container_action", Title: "Start, stop or restart a container", action: true, admin: true,
		Description: "Starts, stops or restarts a Docker container.",
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
		Name: "vm_action", Title: "Start or stop a virtual machine", action: true, admin: true,
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
	{
		Name: "run_command", Title: "Run a command", action: true,
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
			res, err := mcp.RunCommand(c.ctx, acct, cmd, mcp.ClampTimeout(mcp.Int(c.args, "timeout_seconds", 0)), mcp.MaxRunOutput)
			if err != nil {
				return mcp.Errorf("%v", err)
			}
			r := mcp.JSON(res)
			r.IsError = res.TimedOut
			return r
		},
	},
	{
		Name: "open_url", Title: "Open a web page on the screen", action: true, admin: true,
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
		Name: "type_text", Title: "Type on the screen", action: true, admin: true,
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
		Name: "click", Title: "Click on the screen", action: true, admin: true,
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
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// agentAccount is the Linux account run_command uses: the owner's own one
// when NoCapOS runs as root, NoCapOS's own (unprivileged) one otherwise.
// Never root.
func (s *Server) agentAccount(b *agentBackend) (mcp.Account, error) {
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
