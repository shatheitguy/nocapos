package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/vm"
)

func TestVMStopNotification(t *testing.T) {
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	n := vmStopNotification(vm.VM{Name: "Windows 11", UUID: "u-1"}, "it crashed", at)
	if n.Kind != notify.KindAppError || n.Level != "error" || n.Icon != "virtualdesk" || n.Action == nil || n.Action.App != "virtualdesk" {
		t.Errorf("notification %+v", n)
	}
	if n.Title != "Windows 11 stopped unexpectedly" || !strings.Contains(n.Body, "because it crashed") {
		t.Errorf("text %q / %q", n.Title, n.Body)
	}
	if n.Action.Props["vm"] != "Windows 11" || !strings.HasPrefix(n.Key, "vmstop:u-1:") {
		t.Errorf("action %+v key %q", n.Action, n.Key)
	}
	if other := vmStopNotification(vm.VM{UUID: "u-1"}, "x", at.Add(time.Second)); other.Key == n.Key {
		t.Error("each stop is its own notification")
	}
}

func TestVMErrorCodes(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{&vm.Error{Status: http.StatusNotFound, Msg: "gone"}, 404, "not_found"},
		{&vm.Error{Status: http.StatusConflict, Msg: "busy"}, 409, "busy"},
		{&vm.Error{Status: http.StatusServiceUnavailable, Msg: "no kvm"}, 503, "unsupported"},
		{errors.New("virsh failed"), 400, "vm_error"},
	} {
		w := httptest.NewRecorder()
		s.vmError(w, c.err)
		var body struct {
			Error struct{ Code, Message string }
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != c.status || body.Error.Code != c.code || body.Error.Message != c.err.Error() {
			t.Errorf("%v -> %d %+v", c.err, w.Code, body)
		}
	}
}
