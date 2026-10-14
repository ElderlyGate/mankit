//go:build darwin || windows

package desktop

import "github.com/atotto/clipboard"

func systemClipboard(s string) error { return clipboard.WriteAll(s) }

func Paste() (string, error) { return clipboard.ReadAll() }
