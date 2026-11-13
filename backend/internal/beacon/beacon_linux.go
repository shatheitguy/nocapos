//go:build linux

package beacon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// Supported: this build can advertise (when there's an adapter).
const Supported = true

const (
	bluezName   = "org.bluez"
	adapterIf   = "org.bluez.Adapter1"
	advMgrIf    = "org.bluez.LEAdvertisingManager1"
	advIf       = "org.bluez.LEAdvertisement1"
	objectMgrIf = "org.freedesktop.DBus.ObjectManager"
	advPath     = dbus.ObjectPath("/org/nocapos/beacon/ad0")
)

// Available reports whether a powered adapter that can advertise is there.
func Available(ctx context.Context) bool {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return false
	}
	defer conn.Close()
	_, err = findAdapter(ctx, conn)
	return err == nil
}

// findAdapter is the first powered adapter with an advertising manager.
// A switched-off adapter stays off: turning Bluetooth on is the owner's call.
func findAdapter(ctx context.Context, conn *dbus.Conn) (dbus.ObjectPath, error) {
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Object(bluezName, "/").CallWithContext(cctx, objectMgrIf+".GetManagedObjects", 0).Store(&objs); err != nil {
		return "", ErrUnavailable // no Bluetooth daemon
	}
	var paths []string
	for p, ifs := range objs {
		if _, ok := ifs[advMgrIf]; !ok {
			continue
		}
		a, ok := ifs[adapterIf]
		if !ok {
			continue
		}
		if on, _ := a["Powered"].Value().(bool); on {
			paths = append(paths, string(p))
		}
	}
	if len(paths) == 0 {
		return "", ErrUnavailable
	}
	sort.Strings(paths) // hci0 before hci1: the same adapter every time
	return dbus.ObjectPath(paths[0]), nil
}

// advert is the advertisement object the Bluetooth daemon calls back.
type advert struct{ released chan struct{} }

// Release: the daemon dropped the advertisement (adapter gone or reset).
func (a advert) Release() *dbus.Error {
	select {
	case a.released <- struct{}{}:
	default:
	}
	return nil
}

// Run advertises ad until ctx ends (nil) or the adapter goes away (an
// error, so the caller tries again later). ErrUnavailable: no adapter.
func Run(ctx context.Context, ad Ad) error {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return ErrUnavailable
	}
	defer conn.Close()
	adapter, err := findAdapter(ctx, conn)
	if err != nil {
		return err
	}
	a := advert{released: make(chan struct{}, 1)}
	if err := conn.Export(a, advPath, advIf); err != nil {
		return fmt.Errorf("bluetooth advertisement: %w", err)
	}
	// "broadcast": not connectable, so no phone can connect or pair to it.
	props := prop.Map{advIf: {
		"Type":         {Value: "broadcast", Emit: prop.EmitFalse},
		"ServiceUUIDs": {Value: []string{ServiceUUID(ad.SetupNew)}, Emit: prop.EmitFalse},
		"LocalName":    {Value: ShortName(ad.Name), Emit: prop.EmitFalse},
	}}
	if _, err := prop.Export(conn, advPath, props); err != nil {
		return fmt.Errorf("bluetooth advertisement: %w", err)
	}
	mgr := conn.Object(bluezName, adapter)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = mgr.CallWithContext(cctx, advMgrIf+".RegisterAdvertisement", 0, advPath, map[string]dbus.Variant{}).Err
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("bluetooth advertisement: %w", err)
	}
	select {
	case <-ctx.Done():
		uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.CallWithContext(uctx, advMgrIf+".UnregisterAdvertisement", 0, advPath).Err
		ucancel()
		return nil
	case <-a.released:
		return errors.New("bluetooth stopped the advertisement")
	}
}
