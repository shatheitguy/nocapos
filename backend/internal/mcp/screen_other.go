//go:build !linux

package mcp

import "context"

// Screenshot needs NoCap Desktop, which is Linux only.
func (s Screen) Screenshot(context.Context, float64) ([]byte, error) { return nil, ErrNoScreen }

// OpenURL needs NoCap Desktop, which is Linux only.
func (s Screen) OpenURL(_ context.Context, raw string) error {
	if _, err := CheckURL(raw); err != nil {
		return err
	}
	return ErrNoScreen
}

// TypeText needs NoCap Desktop, which is Linux only.
func (s Screen) TypeText(context.Context, string) error { return ErrNoScreen }

// Click needs NoCap Desktop, which is Linux only.
func (s Screen) Click(context.Context, int, int, string) error { return ErrNoScreen }
